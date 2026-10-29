package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	_ "github.com/go-sql-driver/mysql"

	"harness/delta"
	"harness/protocol"
	"harness/record"
	"harness/storage"
)

const schemaVersion = 1

var ddl = []string{
	`CREATE TABLE IF NOT EXISTS durable_schema (
		singleton TINYINT PRIMARY KEY,
		version BIGINT NOT NULL
	)`,
	`INSERT IGNORE INTO durable_schema (singleton, version) VALUES (1, 0)`,
	`CREATE TABLE IF NOT EXISTS durable_metadata (
		singleton TINYINT PRIMARY KEY,
		next_id BIGINT NOT NULL,
		next_seq BIGINT NOT NULL
	)`,
	`INSERT IGNORE INTO durable_metadata (singleton, next_id, next_seq) VALUES (1, 2, 1)`,
	`CREATE TABLE IF NOT EXISTS record_ids (
		id BIGINT PRIMARY KEY,
		record_type VARCHAR(32) NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS conversations (
		id BIGINT PRIMARY KEY,
		owner_conversation_id BIGINT NULL,
		owner_task_id BIGINT NULL,
		record LONGTEXT NOT NULL,
		INDEX conversations_by_owner_conversation (owner_conversation_id, id),
		INDEX conversations_by_owner_task (owner_task_id, id)
	)`,
	`CREATE TABLE IF NOT EXISTS entries (
		id BIGINT PRIMARY KEY,
		conversation_id BIGINT NOT NULL,
		head BIGINT NULL,
		commit_seq BIGINT NOT NULL,
		record LONGTEXT NOT NULL,
		INDEX entries_by_conversation (conversation_id, id),
		INDEX entry_heads_by_conversation (conversation_id, id)
	)`,
	`CREATE TABLE IF NOT EXISTS tasks (
		id BIGINT PRIMARY KEY,
		conversation_id BIGINT NOT NULL,
		kind VARCHAR(255) NOT NULL,
		status VARCHAR(16) NOT NULL,
		abort_requested TINYINT NOT NULL,
		background TINYINT NOT NULL,
		record LONGTEXT NOT NULL,
		INDEX tasks_by_status (status, id),
		INDEX tasks_by_conversation (conversation_id, id),
		INDEX tasks_by_kind (kind, id),
		INDEX tasks_by_abort_requested (abort_requested, id),
		INDEX tasks_by_background (background, id)
	)`,
	`CREATE TABLE IF NOT EXISTS submissions (
		id BIGINT PRIMARY KEY,
		conversation_id BIGINT NOT NULL,
		request_id VARCHAR(255) NULL,
		status VARCHAR(16) NOT NULL,
		record LONGTEXT NOT NULL,
		INDEX submissions_by_request (conversation_id, request_id),
		INDEX submissions_by_conversation (conversation_id, id),
		INDEX submissions_by_status (status, id)
	)`,
	`CREATE TABLE IF NOT EXISTS documents (
		id BIGINT PRIMARY KEY,
		kind VARCHAR(255) NOT NULL,
		family TINYINT NOT NULL,
		key_value VARCHAR(255) NOT NULL,
		scope_kind VARCHAR(16) NOT NULL,
		owner_id BIGINT NOT NULL,
		created_at BIGINT NOT NULL,
		retired_at BIGINT NULL,
		record LONGTEXT NOT NULL,
		INDEX documents_by_address (kind, scope_kind, owner_id, family, key_value, created_at),
		INDEX documents_by_scope (scope_kind, owner_id, id),
		INDEX documents_by_scope_kind (scope_kind, owner_id, kind, id)
	)`,
	`CREATE TABLE IF NOT EXISTS document_revisions (
		document_id BIGINT NOT NULL,
		seq BIGINT NOT NULL,
		kind VARCHAR(8) NOT NULL,
		version BIGINT NOT NULL,
		content LONGTEXT NOT NULL,
		PRIMARY KEY (document_id, seq),
		INDEX document_revisions_by_kind (document_id, kind, seq)
	)`,
}

type Store struct {
	db     *sql.DB
	nextID protocol.RecordID
	closed bool
}

func Open(dsn string) (*Store, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	for _, stmt := range ddl {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			return nil, err
		}
	}
	var version int64
	if err := db.QueryRow(`SELECT version FROM durable_schema WHERE singleton = 1`).Scan(&version); err != nil {
		db.Close()
		return nil, err
	}
	if version > schemaVersion {
		db.Close()
		return nil, fmt.Errorf("mysql schema version %d is newer than supported version %d", version, schemaVersion)
	}
	var nextID int64
	if err := db.QueryRow(`SELECT next_id FROM durable_metadata WHERE singleton = 1`).Scan(&nextID); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, nextID: protocol.RecordID(nextID)}, nil
}

func (s *Store) MintID(ctx context.Context) (protocol.RecordID, error) {
	id := s.nextID
	s.nextID++
	return id, nil
}

func (s *Store) Commit(ctx context.Context, writes []storage.Write, adopt func(protocol.Seq)) (protocol.Seq, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	var seq protocol.Seq
	if err := func() error {
		defer tx.Rollback()
		var nextSeq, metadataNextID int64
		if err := tx.QueryRow(`SELECT next_id, next_seq FROM durable_metadata WHERE singleton = 1`).Scan(&metadataNextID, &nextSeq); err != nil {
			return err
		}
		seq = protocol.Seq(nextSeq)
		for _, w := range writes {
			if err := s.applyWriteTx(tx, w, seq); err != nil {
				return err
			}
		}
		candidate := int64(s.nextID)
		for _, w := range writes {
			if _, id, ok := writeIdentity(w); ok {
				if int64(id)+1 > candidate {
					candidate = int64(id) + 1
				}
			}
		}
		if metadataNextID > candidate {
			candidate = metadataNextID
		}
		if _, err := tx.Exec(`UPDATE durable_metadata SET next_id = ?, next_seq = ? WHERE singleton = 1`, candidate, nextSeq+1); err != nil {
			return err
		}
		return tx.Commit()
	}(); err != nil {
		return 0, err
	}
	adopt(seq)
	return seq, nil
}

func writeIdentity(w storage.Write) (string, protocol.RecordID, bool) {
	switch w := w.(type) {
	case storage.ConversationWrite:
		return "conversation", protocol.RecordID(w.Value.ID), true
	case storage.EntryWrite:
		return "entry", protocol.RecordID(w.Value.ID), true
	case storage.TaskWrite:
		return "task", protocol.RecordID(w.Value.ID), true
	case storage.SubmissionWrite:
		return "submission", protocol.RecordID(w.Value.ID), true
	case storage.DocumentCreateWrite:
		return "document", protocol.RecordID(w.Record.ID), true
	}
	return "", 0, false
}

func (s *Store) applyWriteTx(tx *sql.Tx, w storage.Write, seq protocol.Seq) error {
	switch w := w.(type) {
	case storage.ConversationWrite:
		b, err := json.Marshal(w.Value)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`REPLACE INTO record_ids (id, record_type) VALUES (?, 'conversation')`, w.Value.ID); err != nil {
			return err
		}
		var ownerConv, ownerTask any
		if w.Value.Owner != nil {
			ownerConv, ownerTask = w.Value.Owner.ConversationID, w.Value.Owner.TaskID
		}
		_, err = tx.Exec(`INSERT INTO conversations (id, owner_conversation_id, owner_task_id, record) VALUES (?, ?, ?, ?)`, w.Value.ID, ownerConv, ownerTask, string(b))
		return err
	case storage.EntryWrite:
		b, err := json.Marshal(w.Value)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`REPLACE INTO record_ids (id, record_type) VALUES (?, 'entry')`, w.Value.ID); err != nil {
			return err
		}
		var head any
		if w.Value.Head != nil {
			head = *w.Value.Head
		}
		_, err = tx.Exec(`INSERT INTO entries (id, conversation_id, head, commit_seq, record) VALUES (?, ?, ?, ?, ?)`, w.Value.ID, w.Value.ConversationID, head, seq, string(b))
		return err
	case storage.TaskWrite:
		b, err := json.Marshal(w.Value)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`REPLACE INTO record_ids (id, record_type) VALUES (?, 'task')`, w.Value.ID); err != nil {
			return err
		}
		_, err = tx.Exec(`REPLACE INTO tasks (id, conversation_id, kind, status, abort_requested, background, record) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			w.Value.ID, w.Value.ConversationID, w.Value.Kind, w.Value.State.Status, boolInt(w.Value.AbortRequested), boolInt(w.Value.Background), string(b))
		return err
	case storage.SubmissionWrite:
		b, err := json.Marshal(w.Value)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`REPLACE INTO record_ids (id, record_type) VALUES (?, 'submission')`, w.Value.ID); err != nil {
			return err
		}
		var req any
		if w.Value.RequestID != "" {
			req = w.Value.RequestID
		}
		_, err = tx.Exec(`REPLACE INTO submissions (id, conversation_id, request_id, status, record) VALUES (?, ?, ?, ?, ?)`,
			w.Value.ID, w.Value.ConversationID, req, w.Value.Status, string(b))
		return err
	case storage.DocumentCreateWrite:
		rec := record.DocumentRecordFromCreate(w.Record, seq)
		b, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		scopeKind, ownerID, family, keyVal := docColumns(w.Record)
		if _, err := tx.Exec(`REPLACE INTO record_ids (id, record_type) VALUES (?, 'document')`, w.Record.ID); err != nil {
			return err
		}
		var retired any
		if rec.RetiredAt != nil {
			retired = *rec.RetiredAt
		}
		if _, err := tx.Exec(`INSERT INTO documents (id, kind, family, key_value, scope_kind, owner_id, created_at, retired_at, record) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			rec.ID, rec.Kind, family, keyVal, scopeKind, ownerID, rec.CreatedAt, retired, string(b)); err != nil {
			return err
		}
		content, err := marshalContent(w.Content)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO document_revisions (document_id, seq, kind, version, content) VALUES (?, ?, ?, ?, ?)`,
			w.Record.ID, seq, w.Content.Kind, w.Content.Version, string(content))
		return err
	case storage.DocumentChangeWrite:
		content, err := marshalContent(w.Content)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO document_revisions (document_id, seq, kind, version, content) VALUES (?, ?, ?, ?, ?)`,
			w.ID, seq, w.Content.Kind, w.Content.Version, string(content))
		return err
	case storage.DocumentRetireWrite:
		if _, err := tx.Exec(`UPDATE documents SET retired_at = ? WHERE id = ?`, seq, w.ID); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM document_revisions WHERE document_id = ?`, w.ID)
		return err
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func docColumns(c record.DocumentCreate) (scopeKind string, ownerID int64, family int, keyVal string) {
	scopeKind = c.Scope.Kind
	switch c.Scope.Kind {
	case "conversation":
		ownerID = int64(*c.Scope.ConversationID)
	case "task":
		ownerID = int64(*c.Scope.TaskID)
	}
	if c.Key != "" {
		family = 1
		keyVal = c.Key
	}
	return
}

func (s *Store) Close(ctx context.Context) error {
	s.closed = true
	return s.db.Close()
}

func (s *Store) Conversation(ctx context.Context, id protocol.ConversationID) (*record.ConversationRecord, error) {
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT record FROM conversations WHERE id = ?`, id).Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	var rec record.ConversationRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (s *Store) ScanConversations(ctx context.Context, q storage.ConversationQuery, limit int, cursor storage.Cursor) (storage.Page[record.ConversationRecord], error) {
	clauses := []string{}
	args := []any{}
	if q.OwnerTaskID != nil {
		clauses = append(clauses, "owner_task_id = ?")
		args = append(args, *q.OwnerTaskID)
	} else if q.OwnerConversationID != nil {
		clauses = append(clauses, "owner_conversation_id = ?")
		args = append(args, *q.OwnerConversationID)
	}
	order, after, has, err := decodeCursor(cursor, q.Order)
	if err != nil {
		return storage.Page[record.ConversationRecord]{}, err
	}
	direction := "ASC"
	if order == storage.Descending {
		direction = "DESC"
		if has {
			clauses = append(clauses, "id < ?")
			args = append(args, after)
		}
	} else if has {
		clauses = append(clauses, "id > ?")
		args = append(args, after)
	}
	where := ""
	if len(clauses) > 0 {
		where = " WHERE " + strings.Join(clauses, " AND ")
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, `SELECT id, record FROM conversations`+where+` ORDER BY id `+direction+` LIMIT ?`, args...)
	if err != nil {
		return storage.Page[record.ConversationRecord]{}, err
	}
	defer rows.Close()
	var items []record.ConversationRecord
	var ids []protocol.RecordID
	for rows.Next() {
		var id protocol.ConversationID
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return storage.Page[record.ConversationRecord]{}, err
		}
		var rec record.ConversationRecord
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			return storage.Page[record.ConversationRecord]{}, err
		}
		items = append(items, rec)
		ids = append(ids, protocol.RecordID(id))
	}
	return paged(items, ids, limit, order), nil
}

func (s *Store) Entry(ctx context.Context, id protocol.EntryID) (*storage.EntryAt, error) {
	var raw string
	var seq protocol.Seq
	if err := s.db.QueryRowContext(ctx, `SELECT record, commit_seq FROM entries WHERE id = ?`, id).Scan(&raw, &seq); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	var rec record.EntryRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return nil, err
	}
	return &storage.EntryAt{Entry: rec, CommitSeq: seq}, nil
}

func (s *Store) ConversationEntry(ctx context.Context, conversationID protocol.ConversationID, id protocol.EntryID) (*storage.EntryAt, error) {
	e, err := s.Entry(ctx, id)
	if err != nil || e == nil || e.Entry.ConversationID != conversationID {
		return nil, err
	}
	return e, nil
}

func (s *Store) FindLatestHeadMarker(ctx context.Context, conversationID protocol.ConversationID, atOrBefore *protocol.EntryID) (*record.EntryRecord, error) {
	var raw string
	var err error
	if atOrBefore == nil {
		err = s.db.QueryRowContext(ctx, `SELECT record FROM entries WHERE conversation_id = ? AND head IS NOT NULL ORDER BY id DESC LIMIT 1`, conversationID).Scan(&raw)
	} else {
		err = s.db.QueryRowContext(ctx, `SELECT record FROM entries WHERE conversation_id = ? AND head IS NOT NULL AND id <= ? ORDER BY id DESC LIMIT 1`, conversationID, *atOrBefore).Scan(&raw)
	}
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rec record.EntryRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (s *Store) ScanEntries(ctx context.Context, q storage.EntryQuery, limit int, cursor storage.Cursor) (storage.Page[record.EntryRecord], error) {
	clauses := []string{"conversation_id = ?"}
	args := []any{q.ConversationID}
	if q.MinEntryID != nil {
		clauses = append(clauses, "id >= ?")
		args = append(args, *q.MinEntryID)
	}
	if q.MaxEntryID != nil {
		clauses = append(clauses, "id <= ?")
		args = append(args, *q.MaxEntryID)
	}
	direction := "DESC"
	if q.Order == storage.Ascending {
		direction = "ASC"
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, `SELECT id, record FROM entries WHERE `+strings.Join(clauses, " AND ")+` ORDER BY id `+direction+` LIMIT ?`, args...)
	if err != nil {
		return storage.Page[record.EntryRecord]{}, err
	}
	defer rows.Close()
	var items []record.EntryRecord
	var ids []protocol.RecordID
	for rows.Next() {
		var id protocol.EntryID
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return storage.Page[record.EntryRecord]{}, err
		}
		var rec record.EntryRecord
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			return storage.Page[record.EntryRecord]{}, err
		}
		items = append(items, rec)
		ids = append(ids, protocol.RecordID(id))
	}
	return paged(items, ids, limit, q.Order), nil
}

func (s *Store) Task(ctx context.Context, id protocol.TaskID) (*record.TaskRecord, error) {
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT record FROM tasks WHERE id = ?`, id).Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	var rec record.TaskRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (s *Store) ScanTasks(ctx context.Context, q storage.TaskQuery, limit int, cursor storage.Cursor) (storage.Page[record.TaskRecord], error) {
	clauses := []string{}
	args := []any{}
	if q.ConversationID != nil {
		clauses = append(clauses, "conversation_id = ?")
		args = append(args, *q.ConversationID)
	}
	if q.Kind != "" {
		clauses = append(clauses, "kind = ?")
		args = append(args, q.Kind)
	}
	if q.Status != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, q.Status)
	}
	order, after, has, err := decodeCursor(cursor, q.Order)
	if err != nil {
		return storage.Page[record.TaskRecord]{}, err
	}
	direction := "ASC"
	if order == storage.Descending {
		direction = "DESC"
		if has {
			clauses = append(clauses, "id < ?")
			args = append(args, after)
		}
	} else if has {
		clauses = append(clauses, "id > ?")
		args = append(args, after)
	}
	where := ""
	if len(clauses) > 0 {
		where = " WHERE " + strings.Join(clauses, " AND ")
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, `SELECT id, record FROM tasks`+where+` ORDER BY id `+direction+` LIMIT ?`, args...)
	if err != nil {
		return storage.Page[record.TaskRecord]{}, err
	}
	defer rows.Close()
	var items []record.TaskRecord
	var ids []protocol.RecordID
	for rows.Next() {
		var id protocol.TaskID
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return storage.Page[record.TaskRecord]{}, err
		}
		var rec record.TaskRecord
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			return storage.Page[record.TaskRecord]{}, err
		}
		items = append(items, rec)
		ids = append(ids, protocol.RecordID(id))
	}
	return paged(items, ids, limit, order), nil
}

func (s *Store) Submission(ctx context.Context, id protocol.SubmissionID) (*record.SubmissionRecord, error) {
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT record FROM submissions WHERE id = ?`, id).Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	var rec record.SubmissionRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (s *Store) ScanSubmissions(ctx context.Context, q storage.SubmissionQuery, limit int, cursor storage.Cursor) (storage.Page[record.SubmissionRecord], error) {
	clauses := []string{}
	args := []any{}
	if q.ConversationID != nil {
		clauses = append(clauses, "conversation_id = ?")
		args = append(args, *q.ConversationID)
	}
	if q.Status != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, q.Status)
	}
	order, after, has, err := decodeCursor(cursor, q.Order)
	if err != nil {
		return storage.Page[record.SubmissionRecord]{}, err
	}
	direction := "ASC"
	if order == storage.Descending {
		direction = "DESC"
		if has {
			clauses = append(clauses, "id < ?")
			args = append(args, after)
		}
	} else if has {
		clauses = append(clauses, "id > ?")
		args = append(args, after)
	}
	where := ""
	if len(clauses) > 0 {
		where = " WHERE " + strings.Join(clauses, " AND ")
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, `SELECT id, record FROM submissions`+where+` ORDER BY id `+direction+` LIMIT ?`, args...)
	if err != nil {
		return storage.Page[record.SubmissionRecord]{}, err
	}
	defer rows.Close()
	var items []record.SubmissionRecord
	var ids []protocol.RecordID
	for rows.Next() {
		var id protocol.SubmissionID
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return storage.Page[record.SubmissionRecord]{}, err
		}
		var rec record.SubmissionRecord
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			return storage.Page[record.SubmissionRecord]{}, err
		}
		items = append(items, rec)
		ids = append(ids, protocol.RecordID(id))
	}
	return paged(items, ids, limit, order), nil
}

func (s *Store) SubmissionByRequest(ctx context.Context, conversationID protocol.ConversationID, requestID string) (*record.SubmissionRecord, error) {
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT record FROM submissions WHERE conversation_id = ? AND request_id = ?`, conversationID, requestID).Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	var rec record.SubmissionRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (s *Store) FindDocument(ctx context.Context, address storage.DocumentAddress, at storage.DocumentPoint) (*record.DocumentRecord, error) {
	scopeKind, ownerID, family, keyVal := docColumnsFromScope(address.Scope, address.Key)
	clauses := []string{"kind = ?", "scope_kind = ?", "owner_id = ?", "family = ?", "key_value = ?"}
	args := []any{address.Kind, scopeKind, ownerID, family, keyVal}
	if at.IsCurrent() {
		clauses = append(clauses, "retired_at IS NULL")
	} else {
		clauses = append(clauses, "created_at <= ? AND (retired_at IS NULL OR retired_at > ?)")
		args = append(args, at.Seq(), at.Seq())
	}
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT record FROM documents WHERE `+strings.Join(clauses, " AND ")+` ORDER BY created_at DESC LIMIT 1`, args...).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rec record.DocumentRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (s *Store) Document(ctx context.Context, id protocol.DocumentID, at storage.DocumentPoint) (*storage.StoredDocument, error) {
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT record FROM documents WHERE id = ?`, id).Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	var rec record.DocumentRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return nil, err
	}
	upper := "18446744073709551615"
	if !at.IsCurrent() {
		upper = fmt.Sprintf("%d", at.Seq())
	}
	var baseContent string
	var baseVersion int64
	var baseSeq int64
	err := s.db.QueryRowContext(ctx, `SELECT content, version, seq FROM document_revisions WHERE document_id = ? AND seq <= ? AND kind = 'base' ORDER BY seq DESC LIMIT 1`, id, upper).Scan(&baseContent, &baseVersion, &baseSeq)
	if err != nil {
		return nil, err
	}
	var baseValue any
	if err := json.Unmarshal([]byte(baseContent), &baseValue); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT content, kind, version FROM document_revisions WHERE document_id = ? AND seq > ? AND seq <= ? ORDER BY seq`, id, baseSeq, upper)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var batches [][]delta.Op
	for rows.Next() {
		var content, revKind string
		var version int64
		if err := rows.Scan(&content, &revKind, &version); err != nil {
			return nil, err
		}
		if revKind != "delta" || float64(version) != float64(baseVersion) {
			return nil, fmt.Errorf("document %d crosses a stored version boundary without a base", id)
		}
		ops, err := unmarshalOps([]byte(content))
		if err != nil {
			return nil, err
		}
		batches = append(batches, ops)
	}
	value, err := delta.ApplyImmutableBatches(baseValue, batches)
	if err != nil {
		return nil, err
	}
	return &storage.StoredDocument{Record: rec, Version: float64(baseVersion), Value: value, DeltasSinceBase: len(batches)}, nil
}

func marshalContent(c storage.DocumentContent) ([]byte, error) {
	if c.Kind == "base" {
		return json.Marshal(c.Value)
	}
	return json.Marshal(c.Ops)
}

func unmarshalOps(data []byte) ([]delta.Op, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	ops := make([]delta.Op, 0, len(raw))
	for _, r := range raw {
		op, err := delta.UnmarshalOp(r)
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)
	}
	return ops, nil
}

func (s *Store) ScanDocuments(ctx context.Context, q storage.DocumentQuery, limit int, cursor storage.Cursor) (storage.Page[record.DocumentRecord], error) {
	scopeKind, ownerID, _, _ := docColumnsFromScope(q.Scope, nil)
	clauses := []string{"scope_kind = ?", "owner_id = ?"}
	args := []any{scopeKind, ownerID}
	if q.Kind != "" {
		clauses = append(clauses, "kind = ?")
		args = append(args, q.Kind)
	}
	if q.At.IsCurrent() {
		clauses = append(clauses, "retired_at IS NULL")
	} else {
		clauses = append(clauses, "created_at <= ? AND (retired_at IS NULL OR retired_at > ?)")
		args = append(args, q.At.Seq(), q.At.Seq())
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, `SELECT id, record FROM documents WHERE `+strings.Join(clauses, " AND ")+` ORDER BY id ASC LIMIT ?`, args...)
	if err != nil {
		return storage.Page[record.DocumentRecord]{}, err
	}
	defer rows.Close()
	var items []record.DocumentRecord
	var ids []protocol.RecordID
	for rows.Next() {
		var id protocol.DocumentID
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return storage.Page[record.DocumentRecord]{}, err
		}
		var rec record.DocumentRecord
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			return storage.Page[record.DocumentRecord]{}, err
		}
		items = append(items, rec)
		ids = append(ids, protocol.RecordID(id))
	}
	return paged(items, ids, limit, storage.Ascending), nil
}

func docColumnsFromScope(scope record.DocumentScope, key *string) (string, int64, int, string) {
	scopeKind := scope.Kind
	var ownerID int64
	switch scope.Kind {
	case "conversation":
		ownerID = int64(*scope.ConversationID)
	case "task":
		ownerID = int64(*scope.TaskID)
	}
	family := 0
	keyVal := ""
	if key != nil {
		family = 1
		keyVal = *key
	}
	return scopeKind, ownerID, family, keyVal
}

func decodeCursor(cursor storage.Cursor, fallback storage.ScanOrder) (storage.ScanOrder, protocol.RecordID, bool, error) {
	order := fallback
	if order == "" {
		order = storage.Ascending
	}
	var after protocol.RecordID
	has := false
	if len(cursor) > 0 {
		has = true
		var c struct {
			After protocol.RecordID `json:"after"`
			Order storage.ScanOrder `json:"order"`
		}
		if err := json.Unmarshal(cursor, &c); err != nil {
			return "", 0, false, err
		}
		after = c.After
		if c.Order != "" {
			if fallback != "" && fallback != c.Order {
				return "", 0, false, fmt.Errorf("%w: cursor order mismatch", storage.ErrStorageRequest)
			}
			order = c.Order
		}
	}
	return order, after, has, nil
}

func encodeCursor(id protocol.RecordID, order storage.ScanOrder) storage.Cursor {
	b, _ := json.Marshal(map[string]any{"after": id, "order": order})
	return b
}

func paged[T any](items []T, ids []protocol.RecordID, limit int, order storage.ScanOrder) storage.Page[T] {
	if len(items) <= limit {
		return storage.Page[T]{Items: items}
	}
	return storage.Page[T]{Items: items[:limit], Next: encodeCursor(ids[limit-1], order)}
}
