package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"

	"harness/delta"
	"harness/protocol"
	"harness/record"
	"harness/storage"
)

const (
	schemaVersion = 2

	tableSchema        = "durable_schema"
	tableMetadata      = "durable_metadata"
	tableRecordIDs     = "record_ids"
	tableConversations = "conversations"
	tableEntries       = "entries"
	tableTasks         = "tasks"
	tableSubmissions   = "submissions"
	tableDocuments     = "documents"
	tableRevisions     = "document_revisions"
)

var initialSchema = []string{
	`CREATE TABLE durable_metadata (
		singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
		next_id TEXT NOT NULL,
		next_seq INTEGER NOT NULL
	) STRICT`,
	`INSERT INTO durable_metadata (singleton, next_id, next_seq) VALUES (1, '2', 1)`,
	`CREATE TABLE record_ids (
		id INTEGER PRIMARY KEY,
		record_type TEXT NOT NULL CHECK (record_type IN ('conversation', 'entry', 'task', 'submission', 'document'))
	) STRICT`,
	`CREATE TABLE conversations (
		id INTEGER PRIMARY KEY,
		owner_conversation_id INTEGER,
		owner_task_id INTEGER,
		record TEXT NOT NULL CHECK (json_valid(record))
	) STRICT`,
	`CREATE INDEX conversations_by_owner_conversation ON conversations (owner_conversation_id, id)`,
	`CREATE INDEX conversations_by_owner_task ON conversations (owner_task_id, id)`,
	`CREATE TABLE entries (
		id INTEGER PRIMARY KEY,
		conversation_id INTEGER NOT NULL,
		head INTEGER,
		commit_seq INTEGER NOT NULL,
		record TEXT NOT NULL CHECK (json_valid(record))
	) STRICT`,
	`CREATE INDEX entries_by_conversation ON entries (conversation_id, id DESC)`,
	`CREATE INDEX entry_heads_by_conversation ON entries (conversation_id, id DESC) WHERE head IS NOT NULL`,
	`CREATE TABLE tasks (
		id INTEGER PRIMARY KEY,
		conversation_id INTEGER NOT NULL,
		kind TEXT NOT NULL,
		status TEXT NOT NULL CHECK (status IN ('pending', 'running', 'waiting', 'completing', 'terminal')),
		abort_requested INTEGER NOT NULL CHECK (abort_requested IN (0, 1)),
		background INTEGER NOT NULL CHECK (background IN (0, 1)),
		record TEXT NOT NULL CHECK (json_valid(record))
	) STRICT`,
	`CREATE INDEX tasks_by_status ON tasks (status, id)`,
	`CREATE INDEX tasks_by_conversation ON tasks (conversation_id, id)`,
	`CREATE INDEX tasks_by_kind ON tasks (kind, id)`,
	`CREATE INDEX tasks_by_abort_requested ON tasks (abort_requested, id)`,
	`CREATE INDEX tasks_by_background ON tasks (background, id)`,
	`CREATE TABLE submissions (
		id INTEGER PRIMARY KEY,
		conversation_id INTEGER NOT NULL,
		request_id TEXT,
		status TEXT NOT NULL CHECK (status IN ('queued', 'placed', 'done', 'unanswered')),
		record TEXT NOT NULL CHECK (json_valid(record))
	) STRICT`,
	`CREATE INDEX submissions_by_request ON submissions (conversation_id, request_id)`,
	`CREATE INDEX submissions_by_conversation ON submissions (conversation_id, id)`,
	`CREATE INDEX submissions_by_status ON submissions (status, id)`,
	`CREATE TABLE documents (
		id INTEGER PRIMARY KEY,
		kind TEXT NOT NULL,
		family INTEGER NOT NULL CHECK (family IN (0, 1)),
		key_value TEXT NOT NULL,
		scope_kind TEXT NOT NULL CHECK (scope_kind IN ('session', 'conversation', 'task')),
		owner_id INTEGER NOT NULL,
		created_at INTEGER NOT NULL,
		retired_at INTEGER,
		record TEXT NOT NULL CHECK (json_valid(record))
	) STRICT`,
	`CREATE INDEX documents_by_address ON documents (kind, scope_kind, owner_id, family, key_value, created_at DESC, retired_at)`,
	`CREATE INDEX documents_by_scope ON documents (scope_kind, owner_id, id)`,
	`CREATE INDEX documents_by_scope_kind ON documents (scope_kind, owner_id, kind, id)`,
	`CREATE TABLE document_revisions (
		document_id INTEGER NOT NULL,
		seq INTEGER NOT NULL,
		kind TEXT NOT NULL CHECK (kind IN ('base', 'delta')),
		version INTEGER NOT NULL,
		content TEXT NOT NULL CHECK (json_valid(content)),
		PRIMARY KEY (document_id, seq)
	) STRICT`,
	`CREATE INDEX document_revisions_by_kind ON document_revisions (document_id, kind, seq DESC)`,
}

var sleepingTasksMigration = []string{
	`CREATE TABLE tasks_v2 (
		id INTEGER PRIMARY KEY,
		conversation_id INTEGER NOT NULL,
		kind TEXT NOT NULL,
		status TEXT NOT NULL CHECK (status IN ('pending', 'running', 'waiting', 'sleeping', 'completing', 'terminal')),
		abort_requested INTEGER NOT NULL CHECK (abort_requested IN (0, 1)),
		background INTEGER NOT NULL CHECK (background IN (0, 1)),
		record TEXT NOT NULL CHECK (json_valid(record))
	) STRICT`,
	`INSERT INTO tasks_v2 (id, conversation_id, kind, status, abort_requested, background, record)
		SELECT id, conversation_id, kind, status, abort_requested, background, record FROM tasks`,
	`DROP TABLE tasks`,
	`ALTER TABLE tasks_v2 RENAME TO tasks`,
	`CREATE INDEX tasks_by_status ON tasks (status, id)`,
	`CREATE INDEX tasks_by_conversation ON tasks (conversation_id, id)`,
	`CREATE INDEX tasks_by_kind ON tasks (kind, id)`,
	`CREATE INDEX tasks_by_abort_requested ON tasks (abort_requested, id)`,
	`CREATE INDEX tasks_by_background ON tasks (background, id)`,
}

type Store struct {
	db     *sql.DB
	nextID protocol.RecordID
	closed bool
}

type docAddress struct {
	kind      string
	scopeKind string
	ownerID   int64
	family    int
	keyVal    string
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA journal_mode = WAL"); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec("PRAGMA synchronous = NORMAL"); err != nil {
		db.Close()
		return nil, err
	}
	if err := applyMigrations(db, schemaVersion); err != nil {
		db.Close()
		return nil, err
	}
	var nextID string
	if err := db.QueryRow(`SELECT next_id FROM durable_metadata WHERE singleton = 1`).Scan(&nextID); err != nil {
		db.Close()
		return nil, err
	}
	var idNum uint64
	if _, err := fmt.Sscanf(nextID, "%d", &idNum); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite: bad next_id %q", nextID)
	}
	return &Store{db: db, nextID: protocol.RecordID(idNum)}, nil
}

func applyMigrations(db *sql.DB, upTo int) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS durable_schema (
		singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
		version INTEGER NOT NULL CHECK (version >= 0)
	) STRICT`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO durable_schema (singleton, version) VALUES (1, 0)`); err != nil {
		return err
	}
	var version int
	if err := tx.QueryRow(`SELECT version FROM durable_schema WHERE singleton = 1`).Scan(&version); err != nil {
		return err
	}
	if version > upTo {
		return fmt.Errorf("sqlite schema version %d is newer than supported version %d", version, upTo)
	}
	for v := version + 1; v <= upTo; v++ {
		statements := initialSchema
		if v == 2 {
			statements = sleepingTasksMigration
		}
		for _, s := range statements {
			if _, err := tx.Exec(s); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`UPDATE durable_schema SET version = ? WHERE singleton = 1`, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) assertOpen() error {
	if s.closed {
		return fmt.Errorf("sqlite: store is closed")
	}
	return nil
}

func (s *Store) MintID(ctx context.Context) (protocol.RecordID, error) {
	if err := s.assertOpen(); err != nil {
		return 0, err
	}
	id := s.nextID
	s.nextID++
	return id, nil
}

func (s *Store) Commit(ctx context.Context, writes []storage.Write, adopt func(protocol.Seq)) (protocol.Seq, error) {
	if err := s.assertOpen(); err != nil {
		return 0, err
	}
	resolved, err := s.resolveCopies(ctx, writes)
	if err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	var committedSeq protocol.Seq
	if err := func() error {
		defer tx.Rollback()
		var nextSeq int64
		var nextIDText string
		if err := tx.QueryRow(`SELECT next_id, next_seq FROM durable_metadata WHERE singleton = 1`).Scan(&nextIDText, &nextSeq); err != nil {
			return err
		}
		committedSeq = protocol.Seq(nextSeq)
		if err := s.checkGlobalIDsTx(tx, resolved); err != nil {
			return err
		}
		if err := s.checkDocumentActionsTx(tx, resolved, committedSeq); err != nil {
			return err
		}
		for _, w := range resolved {
			if err := s.applyWriteTx(tx, w, committedSeq); err != nil {
				return err
			}
		}
		if err := s.applyDocumentActionsTx(tx, resolved, committedSeq); err != nil {
			return err
		}
		candidate := s.nextID
		for _, w := range resolved {
			if _, id, ok := writeIdentity(w); ok {
				if id+1 > candidate {
					candidate = id + 1
				}
			}
		}
		var metadataNextID int64
		if _, err := fmt.Sscanf(nextIDText, "%d", &metadataNextID); err != nil {
			return err
		}
		next := int64(candidate)
		if metadataNextID > next {
			next = metadataNextID
		}
		if _, err := tx.Exec(`UPDATE durable_metadata SET next_id = ?, next_seq = ? WHERE singleton = 1`, fmt.Sprintf("%d", next), nextSeq+1); err != nil {
			return err
		}
		return tx.Commit()
	}(); err != nil {
		return 0, err
	}
	for _, w := range resolved {
		if _, id, ok := writeIdentity(w); ok {
			if id+1 > s.nextID {
				s.nextID = id + 1
			}
		}
	}
	adopt(committedSeq)
	return committedSeq, nil
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
	case storage.DocumentCopyWrite:
		return "document", protocol.RecordID(w.Record.ID), true
	}
	return "", 0, false
}

func (s *Store) checkGlobalIDsTx(tx *sql.Tx, writes []storage.Write) error {
	claimed := map[protocol.RecordID]string{}
	for _, w := range writes {
		table, id, ok := writeIdentity(w)
		if !ok {
			continue
		}
		var existing string
		err := tx.QueryRow(`SELECT record_type FROM record_ids WHERE id = ?`, id).Scan(&existing)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		earlier := claimed[id]
		if table == "conversation" || table == "entry" || table == "document" {
			if err == nil {
				return fmt.Errorf("ID %d already belongs to %s", id, existing)
			}
			if earlier != "" {
				return fmt.Errorf("ID %d is written more than once", id)
			}
		} else {
			if err == nil && existing != table {
				return fmt.Errorf("ID %d already belongs to %s", id, existing)
			}
			if earlier != "" && earlier != table {
				return fmt.Errorf("ID %d is written as two record types", id)
			}
		}
		claimed[id] = table
	}
	return nil
}

func (s *Store) applyWriteTx(tx *sql.Tx, w storage.Write, seq protocol.Seq) error {
	switch w := w.(type) {
	case storage.ConversationWrite:
		b, err := json.Marshal(w.Value)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO record_ids (id, record_type) VALUES (?, 'conversation')`, w.Value.ID); err != nil {
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
		if _, err := tx.Exec(`INSERT OR REPLACE INTO record_ids (id, record_type) VALUES (?, 'entry')`, w.Value.ID); err != nil {
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
		if _, err := tx.Exec(`INSERT OR REPLACE INTO record_ids (id, record_type) VALUES (?, 'task')`, w.Value.ID); err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT OR REPLACE INTO tasks (id, conversation_id, kind, status, abort_requested, background, record) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			w.Value.ID, w.Value.ConversationID, w.Value.Kind, w.Value.State.Status, boolInt(w.Value.AbortRequested), boolInt(w.Value.Background), string(b))
		return err
	case storage.SubmissionWrite:
		b, err := json.Marshal(w.Value)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO record_ids (id, record_type) VALUES (?, 'submission')`, w.Value.ID); err != nil {
			return err
		}
		var req any
		if w.Value.RequestID != "" {
			req = w.Value.RequestID
		}
		_, err = tx.Exec(`INSERT OR REPLACE INTO submissions (id, conversation_id, request_id, status, record) VALUES (?, ?, ?, ?, ?)`,
			w.Value.ID, w.Value.ConversationID, req, w.Value.Status, string(b))
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

func (s *Store) resolveCopies(ctx context.Context, writes []storage.Write) ([]storage.Write, error) {
	hasCopy := false
	changed := map[protocol.DocumentID]bool{}
	for _, w := range writes {
		switch w := w.(type) {
		case storage.DocumentCreateWrite:
			changed[w.Record.ID] = true
		case storage.DocumentCopyWrite:
			hasCopy = true
			changed[w.Record.ID] = true
		case storage.DocumentChangeWrite:
			changed[w.ID] = true
		case storage.DocumentRetireWrite:
			changed[w.ID] = true
		}
	}
	if !hasCopy {
		return writes, nil
	}
	out := make([]storage.Write, 0, len(writes))
	for _, w := range writes {
		cw, ok := w.(storage.DocumentCopyWrite)
		if !ok {
			out = append(out, w)
			continue
		}
		if changed[cw.Source.ID] {
			return nil, fmt.Errorf("fork source document %d is changed in the copy batch", cw.Source.ID)
		}
		stored, err := s.materialize(ctx, cw.Source.ID, cw.Source.At)
		if err != nil || stored == nil {
			return nil, fmt.Errorf("fork source document %d cannot be read", cw.Source.ID)
		}
		if stored.Record.Scope.Kind != "conversation" || cw.Record.Scope.Kind != "conversation" ||
			stored.Record.Kind != cw.Record.Kind || stored.Record.Key != cw.Record.Key ||
			stored.Record.History != cw.Record.History || stored.Record.Fork != cw.Record.Fork {
			return nil, fmt.Errorf("fork source document %d does not match the copied record", cw.Source.ID)
		}
		out = append(out, storage.DocumentCreateWrite{
			Record:  cw.Record,
			Content: storage.DocumentContent{Version: stored.Version, Kind: "base", Value: stored.Value},
		})
	}
	return out, nil
}

func (s *Store) checkDocumentActionsTx(tx *sql.Tx, writes []storage.Write, seq protocol.Seq) error {
	type keyState struct {
		preCurrent     *protocol.DocumentID
		retiredCurrent bool
		newCurrents    int
	}
	states := map[docAddress]*keyState{}
	for _, w := range writes {
		var id protocol.DocumentID
		var create *record.DocumentCreate
		var content *storage.DocumentContent
		var retire bool
		switch w := w.(type) {
		case storage.DocumentCreateWrite:
			id = w.Record.ID
			r := w.Record
			create = &r
			c := w.Content
			content = &c
		case storage.DocumentChangeWrite:
			id = w.ID
			c := w.Content
			content = &c
		case storage.DocumentRetireWrite:
			id = w.ID
			retire = true
		default:
			continue
		}
		var existingRecord string
		var existingScopeKind string
		var existingOwnerID int64
		var existingKind string
		var existingFamily int
		var existingKey string
		var existingRetired sql.NullInt64
		err := tx.QueryRow(`SELECT record, scope_kind, owner_id, kind, family, key_value, retired_at FROM documents WHERE id = ?`, id).
			Scan(&existingRecord, &existingScopeKind, &existingOwnerID, &existingKind, &existingFamily, &existingKey, &existingRetired)
		exists := err == nil
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if create == nil && !exists {
			return fmt.Errorf("unknown document: %d", id)
		}
		if create != nil && exists {
			return fmt.Errorf("document %d already exists", id)
		}
		if exists && existingRetired.Valid {
			return fmt.Errorf("document %d is retired", id)
		}
		if content != nil && content.Kind == "delta" {
			var prevVersion int64
			err := tx.QueryRow(`SELECT version FROM document_revisions WHERE document_id = ? ORDER BY seq DESC LIMIT 1`, id).Scan(&prevVersion)
			if err == sql.ErrNoRows {
				return fmt.Errorf("document %d delta has no base", id)
			}
			if err != nil {
				return err
			}
			if float64(prevVersion) != content.Version {
				return fmt.Errorf("document %d version transition requires a base", id)
			}
		}
		var key docAddress
		if create != nil {
			key = addressOfCreate(*create)
		} else {
			key = docAddress{existingKind, existingScopeKind, existingOwnerID, existingFamily, existingKey}
		}
		ks := states[key]
		if ks == nil {
			ks = &keyState{}
			ks.preCurrent = s.currentDocumentID(tx, key)
			states[key] = ks
		}
		if retire && ks.preCurrent != nil && *ks.preCurrent == id {
			ks.retiredCurrent = true
		}
		if create != nil && !retire {
			ks.newCurrents++
		}
	}
	for _, ks := range states {
		live := 0
		if ks.preCurrent != nil && !ks.retiredCurrent {
			live = 1
		}
		live += ks.newCurrents
		if live > 1 {
			return fmt.Errorf("document address already has a current incarnation")
		}
	}
	return nil
}

func addressOfCreate(c record.DocumentCreate) docAddress {
	var out docAddress
	out.kind = c.Kind
	out.scopeKind = c.Scope.Kind
	switch c.Scope.Kind {
	case "conversation":
		out.ownerID = int64(*c.Scope.ConversationID)
	case "task":
		out.ownerID = int64(*c.Scope.TaskID)
	}
	if c.Key != "" {
		out.family = 1
		out.keyVal = c.Key
	}
	return out
}

func (s *Store) currentDocumentID(tx *sql.Tx, a docAddress) *protocol.DocumentID {
	var id protocol.DocumentID
	err := tx.QueryRow(`SELECT id FROM documents WHERE kind = ? AND scope_kind = ? AND owner_id = ? AND family = ? AND key_value = ? AND retired_at IS NULL ORDER BY created_at DESC LIMIT 1`,
		a.kind, a.scopeKind, a.ownerID, a.family, a.keyVal).Scan(&id)
	if err == sql.ErrNoRows {
		return nil
	}
	return &id
}

func (s *Store) applyDocumentActionsTx(tx *sql.Tx, writes []storage.Write, seq protocol.Seq) error {
	for _, w := range writes {
		switch w := w.(type) {
		case storage.DocumentCreateWrite:
			rec := record.DocumentRecordFromCreate(w.Record, seq)
			scopeKind, ownerID, family, keyVal := docColumns(w.Record)
			if err := s.insertDocument(tx, rec, scopeKind, ownerID, family, keyVal); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT OR REPLACE INTO record_ids (id, record_type) VALUES (?, 'document')`, w.Record.ID); err != nil {
				return err
			}
			b, err := marshalContent(w.Content)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO document_revisions (document_id, seq, kind, version, content) VALUES (?, ?, ?, ?, ?)`,
				w.Record.ID, seq, w.Content.Kind, w.Content.Version, string(b)); err != nil {
				return err
			}
		case storage.DocumentChangeWrite:
			_, err := tx.Exec(`DELETE FROM document_revisions WHERE document_id = ? AND kind = 'delta' AND seq = ?`, w.ID, seq)
			if err != nil {
				return err
			}
			b, err := marshalContent(w.Content)
			if err != nil {
				return err
			}
			_, err = tx.Exec(`INSERT INTO document_revisions (document_id, seq, kind, version, content) VALUES (?, ?, ?, ?, ?)`,
				w.ID, seq, w.Content.Kind, w.Content.Version, string(b))
			if err != nil {
				return err
			}
			if w.Content.Kind == "base" {
				var raw string
				if err := tx.QueryRow(`SELECT record FROM documents WHERE id = ?`, w.ID).Scan(&raw); err != nil {
					return err
				}
				var rec record.DocumentRecord
				if err := json.Unmarshal([]byte(raw), &rec); err != nil {
					return err
				}
				if rec.Scope.Kind != "conversation" || rec.History == "latest" {
					if _, err := tx.Exec(`DELETE FROM document_revisions WHERE document_id = ? AND seq < ?`, w.ID, seq); err != nil {
						return err
					}
				}
			}
		case storage.DocumentRetireWrite:
			var raw string
			if err := tx.QueryRow(`SELECT record FROM documents WHERE id = ?`, w.ID).Scan(&raw); err != nil {
				return err
			}
			var rec record.DocumentRecord
			if err := json.Unmarshal([]byte(raw), &rec); err != nil {
				return err
			}
			retired := seq
			rec.RetiredAt = &retired
			b, err := json.Marshal(rec)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE documents SET retired_at = ?, record = ? WHERE id = ?`, seq, string(b), w.ID); err != nil {
				return err
			}
			if rec.Scope.Kind != "conversation" || rec.History == "latest" {
				if _, err := tx.Exec(`DELETE FROM document_revisions WHERE document_id = ?`, w.ID); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *Store) insertDocument(tx *sql.Tx, rec record.DocumentRecord, scopeKind string, ownerID int64, family int, keyVal string) error {
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	var retired any
	if rec.RetiredAt != nil {
		retired = *rec.RetiredAt
	}
	_, err = tx.Exec(`INSERT INTO documents (id, kind, family, key_value, scope_kind, owner_id, created_at, retired_at, record) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.ID, rec.Kind, family, keyVal, scopeKind, ownerID, rec.CreatedAt, retired, string(b))
	return err
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

func addressKey(kind, scopeKind string, ownerID int64, family int, keyVal string) string {
	return fmt.Sprintf("%s|%s|%d|%d|%s", kind, scopeKind, ownerID, family, keyVal)
}

func addressKeyFromCreate(c record.DocumentCreate) string {
	scopeKind, ownerID, family, keyVal := docColumns(c)
	return addressKey(c.Kind, scopeKind, ownerID, family, keyVal)
}

func (s *Store) materialize(ctx context.Context, id protocol.DocumentID, at storage.DocumentPoint) (*storage.StoredDocument, error) {
	var recordJSON, scopeKind string
	var ownerID int64
	var createdAt int64
	var retiredAt sql.NullInt64
	var family int
	var keyVal, kind, history, fork string
	err := s.db.QueryRow(`SELECT record, kind, family, key_value, scope_kind, owner_id, created_at, retired_at FROM documents WHERE id = ?`, id).
		Scan(&recordJSON, &kind, &family, &keyVal, &scopeKind, &ownerID, &createdAt, &retiredAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rec record.DocumentRecord
	if err := json.Unmarshal([]byte(recordJSON), &rec); err != nil {
		return nil, err
	}
	_ = history
	_ = fork
	if !at.IsCurrent() && (scopeKind != "conversation" || rec.History == "latest") {
		return nil, fmt.Errorf("%w: document %d does not retain historical content", storage.ErrStorageRequest, id)
	}
	if !aliveAt(rec, at) {
		return nil, nil
	}
	upper := "9223372036854775807"
	if !at.IsCurrent() {
		upper = fmt.Sprintf("%d", at.Seq())
	}
	var baseContent string
	var baseVersion int64
	var baseSeq int64
	err = s.db.QueryRow(`SELECT content, version, seq FROM document_revisions WHERE document_id = ? AND seq <= ? AND kind = 'base' ORDER BY seq DESC LIMIT 1`, id, upper).
		Scan(&baseContent, &baseVersion, &baseSeq)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("document %d is missing a required base", id)
	}
	if err != nil {
		return nil, err
	}
	var baseValue any
	if err := json.Unmarshal([]byte(baseContent), &baseValue); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT content, kind, version FROM document_revisions WHERE document_id = ? AND seq > ? AND seq <= ? ORDER BY seq`, id, baseSeq, upper)
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
	if err := rows.Err(); err != nil {
		return nil, err
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

func aliveAt(r record.DocumentRecord, at storage.DocumentPoint) bool {
	if at.IsCurrent() {
		return r.RetiredAt == nil
	}
	atSeq := uint64(at.Seq())
	return uint64(r.CreatedAt) <= atSeq && (r.RetiredAt == nil || atSeq < uint64(*r.RetiredAt))
}

func (s *Store) Close(ctx context.Context) error {
	s.closed = true
	return s.db.Close()
}

// ─── Reads ──────────────────────────────────────────────────────────────────

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
	query := `SELECT id, record FROM conversations` + where + ` ORDER BY id ` + direction + ` LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return storage.Page[record.ConversationRecord]{}, err
	}
	defer rows.Close()
	items := make([]record.ConversationRecord, 0)
	var ids []protocol.RecordID
	for rows.Next() {
		var raw string
		var id protocol.ConversationID
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

func page[T any](items []T, limit int) storage.Page[T] {
	if len(items) <= limit {
		return storage.Page[T]{Items: items}
	}
	return storage.Page[T]{Items: items[:limit]}
}

func paged[T any](items []T, ids []protocol.RecordID, limit int, order storage.ScanOrder) storage.Page[T] {
	if len(items) <= limit {
		return storage.Page[T]{Items: items}
	}
	return storage.Page[T]{Items: items[:limit], Next: encodeCursor(ids[limit-1], order)}
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
	entries, err := s.visibleEntries(ctx, conversationID, &id, &id, false)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	var commitSeq protocol.Seq
	if err := s.db.QueryRowContext(ctx, `SELECT commit_seq FROM entries WHERE id = ?`, id).Scan(&commitSeq); err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	return &storage.EntryAt{Entry: entries[0], CommitSeq: commitSeq}, nil
}

func (s *Store) visibleEntries(ctx context.Context, conversationID protocol.ConversationID, minID, maxID *protocol.EntryID, ascending bool) ([]record.EntryRecord, error) {
	rec, err := s.Conversation(ctx, conversationID)
	if err != nil || rec == nil {
		return nil, fmt.Errorf("%w: unknown conversation %d", storage.ErrStorageRequest, conversationID)
	}
	type segment struct {
		id    protocol.ConversationID
		upper *protocol.EntryID
	}
	segments := make([]segment, 0, 4)
	current := conversationID
	upper := maxID
	for {
		segments = append(segments, segment{id: current, upper: upper})
		cur, err := s.Conversation(ctx, current)
		if err != nil {
			return nil, err
		}
		if cur.Parent == nil {
			break
		}
		if upper == nil || cur.Parent.At < *upper {
			upper = &cur.Parent.At
		}
		if minID != nil && upper != nil && *upper < *minID {
			break
		}
		current = cur.Parent.ConversationID
	}
	if ascending {
		for i, j := 0, len(segments)-1; i < j; i, j = i+1, j-1 {
			segments[i], segments[j] = segments[j], segments[i]
		}
	}
	var out []record.EntryRecord
	for _, seg := range segments {
		clauses := []string{"conversation_id = ?"}
		args := []any{seg.id}
		if minID != nil {
			clauses = append(clauses, "id >= ?")
			args = append(args, *minID)
		}
		upperBound := seg.upper
		if maxID != nil && (upperBound == nil || *maxID < *upperBound) {
			upperBound = maxID
		}
		if upperBound != nil {
			clauses = append(clauses, "id <= ?")
			args = append(args, *upperBound)
		}
		dir := "DESC"
		if ascending {
			dir = "ASC"
		}
		q := `SELECT record FROM entries WHERE ` + strings.Join(clauses, " AND ") + ` ORDER BY id ` + dir
		rows, err := s.db.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var raw string
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				return nil, err
			}
			var e record.EntryRecord
			if err := json.Unmarshal([]byte(raw), &e); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, e)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) FindLatestHeadMarker(ctx context.Context, conversationID protocol.ConversationID, atOrBefore *protocol.EntryID) (*record.EntryRecord, error) {
	current := conversationID
	upper := atOrBefore
	for {
		var raw string
		var err error
		if upper == nil {
			err = s.db.QueryRowContext(ctx, `SELECT record FROM entries WHERE conversation_id = ? AND head IS NOT NULL ORDER BY id DESC LIMIT 1`, current).Scan(&raw)
		} else {
			err = s.db.QueryRowContext(ctx, `SELECT record FROM entries WHERE conversation_id = ? AND head IS NOT NULL AND id <= ? ORDER BY id DESC LIMIT 1`, current, *upper).Scan(&raw)
		}
		if err == nil {
			var rec record.EntryRecord
			if err := json.Unmarshal([]byte(raw), &rec); err != nil {
				return nil, err
			}
			return &rec, nil
		}
		if err != sql.ErrNoRows {
			return nil, err
		}
		conv, err := s.Conversation(ctx, current)
		if err != nil {
			return nil, err
		}
		if conv.Parent == nil {
			return nil, nil
		}
		if upper == nil || conv.Parent.At < *upper {
			upper = &conv.Parent.At
		}
		current = conv.Parent.ConversationID
	}
}

func (s *Store) ScanEntries(ctx context.Context, q storage.EntryQuery, limit int, cursor storage.Cursor) (storage.Page[record.EntryRecord], error) {
	entries, err := s.visibleEntries(ctx, q.ConversationID, q.MinEntryID, q.MaxEntryID, q.Order == storage.Ascending)
	if err != nil {
		return storage.Page[record.EntryRecord]{}, err
	}
	return page(entries, limit), nil
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
	if q.AbortRequested != nil {
		clauses = append(clauses, "abort_requested = ?")
		args = append(args, boolInt(*q.AbortRequested))
	}
	if q.Background != nil {
		clauses = append(clauses, "background = ?")
		args = append(args, boolInt(*q.Background))
	}
	where := ""
	if len(clauses) > 0 {
		where = " WHERE " + strings.Join(clauses, " AND ")
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
	if len(clauses) > 0 {
		where = " WHERE " + strings.Join(clauses, " AND ")
	} else {
		where = ""
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, `SELECT id, record FROM tasks`+where+` ORDER BY id `+direction+` LIMIT ?`, args...)
	if err != nil {
		return storage.Page[record.TaskRecord]{}, err
	}
	defer rows.Close()
	items := make([]record.TaskRecord, 0)
	var ids []protocol.RecordID
	for rows.Next() {
		var raw string
		var id protocol.TaskID
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
	where := " WHERE " + strings.Join(clauses, " AND ")
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, `SELECT id, record FROM submissions`+where+` ORDER BY id `+direction+` LIMIT ?`, args...)
	if err != nil {
		return storage.Page[record.SubmissionRecord]{}, err
	}
	defer rows.Close()
	items := make([]record.SubmissionRecord, 0)
	var ids []protocol.RecordID
	for rows.Next() {
		var raw string
		var id protocol.SubmissionID
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
	var rec record.DocumentRecord
	scopeKind, ownerID, family, keyVal := docColumnsFromScope(address.Scope, address.Key)
	query := `SELECT record FROM documents WHERE kind = ? AND scope_kind = ? AND owner_id = ? AND family = ? AND key_value = ?`
	args := []any{address.Kind, scopeKind, ownerID, family, keyVal}
	if at.IsCurrent() {
		query += ` AND retired_at IS NULL`
	} else {
		query += ` AND created_at <= ? AND (retired_at IS NULL OR retired_at > ?)`
		args = append(args, at.Seq(), at.Seq())
	}
	var raw string
	err := s.db.QueryRowContext(ctx, query+` ORDER BY created_at DESC LIMIT 1`, args...).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return nil, err
	}
	return &rec, nil
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

func (s *Store) Document(ctx context.Context, id protocol.DocumentID, at storage.DocumentPoint) (*storage.StoredDocument, error) {
	return s.materialize(ctx, id, at)
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
	_, after, has, err := decodeCursor(cursor, storage.Ascending)
	if err != nil {
		return storage.Page[record.DocumentRecord]{}, err
	}
	if has {
		clauses = append(clauses, "id > ?")
		args = append(args, after)
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, `SELECT id, record FROM documents WHERE `+strings.Join(clauses, " AND ")+` ORDER BY id ASC LIMIT ?`, args...)
	if err != nil {
		return storage.Page[record.DocumentRecord]{}, err
	}
	defer rows.Close()
	items := make([]record.DocumentRecord, 0)
	var ids []protocol.RecordID
	for rows.Next() {
		var raw string
		var id protocol.DocumentID
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
