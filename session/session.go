package session

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"harness/delta"
	"harness/protocol"
	"harness/record"
	"harness/storage"
)

// Session owns one mutation line over a Storage. All writes flow through one
// atomic commit; reads go straight to the backend.
type Session struct {
	store storage.Storage
	mu    sync.Mutex
}

func New(store storage.Storage) *Session {
	return &Session{store: store}
}

func (s *Session) Commit(ctx context.Context, change func(tx *Tx) error) (protocol.Seq, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx := &Tx{store: s.store}
	if change != nil {
		if err := change(tx); err != nil {
			return 0, err
		}
	}
	return s.store.Commit(ctx, tx.writes, func(protocol.Seq) {})
}

func (s *Session) MintID(ctx context.Context) (protocol.RecordID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.MintID(ctx)
}

// CreateConversation atomically writes a new conversation record.
func (s *Session) CreateConversation(ctx context.Context, rec record.ConversationRecord) (protocol.Seq, error) {
	return s.Commit(ctx, func(tx *Tx) error {
		if !rec.ID.Valid() {
			return fmt.Errorf("conversation id must be a positive safe integer")
		}
		tx.Conversation(rec)
		return nil
	})
}

// Fork creates a child conversation that inherits the parent's history up to
// and including the entry at `at`.
func (s *Session) Fork(ctx context.Context, parentID protocol.ConversationID, at protocol.EntryID, childID protocol.ConversationID) (protocol.Seq, error) {
	return s.Commit(ctx, func(tx *Tx) error {
		tx.Conversation(record.ConversationRecord{
			ConversationFields: record.ConversationFields{
				ID:     childID,
				Parent: &record.ConversationParent{ConversationID: parentID, At: at},
			},
		})
		copies, err := tx.forkCopies(ctx, parentID, at, childID)
		if err != nil {
			return err
		}
		for _, copy := range copies {
			tx.CopyDocument(copy.record, copy.source)
		}
		return nil
	})
}

// Tx accumulates writes for one atomic commit.
type Tx struct {
	writes []storage.Write
	store  storage.Storage
}

func (t *Tx) Conversation(rec record.ConversationRecord) {
	t.writes = append(t.writes, storage.ConversationWrite{Value: rec})
}

func (t *Tx) Entry(rec record.EntryRecord) {
	t.writes = append(t.writes, storage.EntryWrite{Value: rec})
}

func (t *Tx) Task(rec record.TaskRecord) {
	t.writes = append(t.writes, storage.TaskWrite{Value: rec})
}

func (t *Tx) Submission(rec record.SubmissionRecord) {
	t.writes = append(t.writes, storage.SubmissionWrite{Value: rec})
}

func (t *Tx) CreateDocument(rec record.DocumentCreate, content storage.DocumentContent) {
	t.writes = append(t.writes, storage.DocumentCreateWrite{Record: rec, Content: content})
}

func (t *Tx) ChangeDocument(id protocol.DocumentID, content storage.DocumentContent) {
	t.writes = append(t.writes, storage.DocumentChangeWrite{ID: id, Content: content})
}

func (t *Tx) RetireDocument(id protocol.DocumentID) {
	t.writes = append(t.writes, storage.DocumentRetireWrite{ID: id})
}

// CopyDocument stages a forked conversation document: the backend materializes
// the source at its point and stores it as a new independent base.
func (t *Tx) CopyDocument(rec record.DocumentCreate, source storage.DocumentCopySource) {
	t.writes = append(t.writes, storage.DocumentCopyWrite{Record: rec, Source: source})
}

// MintID allocates an ID without leaving the surrounding commit.
func (t *Tx) MintID(ctx context.Context) (protocol.RecordID, error) {
	return t.store.MintID(ctx)
}

// ConversationEntry reads an entry visible from a conversation.
func (t *Tx) ConversationEntry(ctx context.Context, conversationID protocol.ConversationID, id protocol.EntryID) (*storage.EntryAt, error) {
	return t.store.ConversationEntry(ctx, conversationID, id)
}

// ScanDocuments scans document records through the mutation line.
func (t *Tx) ScanDocuments(ctx context.Context, q storage.DocumentQuery, limit int, cursor storage.Cursor) (storage.Page[record.DocumentRecord], error) {
	return t.store.ScanDocuments(ctx, q, limit, cursor)
}

// Document reads the committed value of a document incarnation through the
// mutation line.
func (t *Tx) Document(ctx context.Context, id protocol.DocumentID, at storage.DocumentPoint) (*storage.StoredDocument, error) {
	return t.store.Document(ctx, id, at)
}

// EditDocument reads the current document value, hands fn a detached copy,
// and records the diff as a document change. It works only on an existing
// incarnation; new incarnations use CreateDocument.
func (t *Tx) EditDocument(ctx context.Context, id protocol.DocumentID, fn func(current any) (any, error)) error {
	doc, err := t.store.Document(ctx, id, storage.CurrentPoint())
	if err != nil {
		return err
	}
	if doc == nil {
		return fmt.Errorf("document %d does not exist", id)
	}
	current := cloneAny(doc.Value)
	next, err := fn(current)
	if err != nil {
		return err
	}
	ops := delta.DiffRevisions(doc.Value, next)
	if len(ops) == 0 {
		return nil
	}
	t.writes = append(t.writes, storage.DocumentChangeWrite{
		ID:      id,
		Content: storage.DocumentContent{Version: doc.Version, Kind: "delta", Ops: ops},
	})
	return nil
}

func cloneAny(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return v
	}
	return out
}

// ── Reads ──────────────────────────────────────────────────────────────────

func (s *Session) Conversation(ctx context.Context, id protocol.ConversationID) (*record.ConversationRecord, error) {
	return s.store.Conversation(ctx, id)
}
func (s *Session) ScanConversations(ctx context.Context, q storage.ConversationQuery, limit int, cursor storage.Cursor) (storage.Page[record.ConversationRecord], error) {
	return s.store.ScanConversations(ctx, q, limit, cursor)
}
func (s *Session) Entry(ctx context.Context, id protocol.EntryID) (*storage.EntryAt, error) {
	return s.store.Entry(ctx, id)
}
func (s *Session) ConversationEntry(ctx context.Context, conversationID protocol.ConversationID, id protocol.EntryID) (*storage.EntryAt, error) {
	return s.store.ConversationEntry(ctx, conversationID, id)
}
func (s *Session) FindLatestHeadMarker(ctx context.Context, conversationID protocol.ConversationID, atOrBefore *protocol.EntryID) (*record.EntryRecord, error) {
	return s.store.FindLatestHeadMarker(ctx, conversationID, atOrBefore)
}
func (s *Session) ScanEntries(ctx context.Context, q storage.EntryQuery, limit int, cursor storage.Cursor) (storage.Page[record.EntryRecord], error) {
	return s.store.ScanEntries(ctx, q, limit, cursor)
}
func (s *Session) Task(ctx context.Context, id protocol.TaskID) (*record.TaskRecord, error) {
	return s.store.Task(ctx, id)
}
func (s *Session) ScanTasks(ctx context.Context, q storage.TaskQuery, limit int, cursor storage.Cursor) (storage.Page[record.TaskRecord], error) {
	return s.store.ScanTasks(ctx, q, limit, cursor)
}
func (s *Session) Submission(ctx context.Context, id protocol.SubmissionID) (*record.SubmissionRecord, error) {
	return s.store.Submission(ctx, id)
}
func (s *Session) ScanSubmissions(ctx context.Context, q storage.SubmissionQuery, limit int, cursor storage.Cursor) (storage.Page[record.SubmissionRecord], error) {
	return s.store.ScanSubmissions(ctx, q, limit, cursor)
}
func (s *Session) SubmissionByRequest(ctx context.Context, conversationID protocol.ConversationID, requestID string) (*record.SubmissionRecord, error) {
	return s.store.SubmissionByRequest(ctx, conversationID, requestID)
}
func (s *Session) FindDocument(ctx context.Context, address storage.DocumentAddress, at storage.DocumentPoint) (*record.DocumentRecord, error) {
	return s.store.FindDocument(ctx, address, at)
}
func (s *Session) Document(ctx context.Context, id protocol.DocumentID, at storage.DocumentPoint) (*storage.StoredDocument, error) {
	return s.store.Document(ctx, id, at)
}
func (s *Session) ScanDocuments(ctx context.Context, q storage.DocumentQuery, limit int, cursor storage.Cursor) (storage.Page[record.DocumentRecord], error) {
	return s.store.ScanDocuments(ctx, q, limit, cursor)
}

func (s *Session) Close(ctx context.Context) error {
	return s.store.Close(ctx)
}

type forkCopy struct {
	record record.DocumentCreate
	source storage.DocumentCopySource
}

// forkCopies selects the conversation-scoped documents a fork must copy, first
// the asOf-policy documents at the fork entry's commit sequence, then the
// current-policy documents at the current point.
func (t *Tx) forkCopies(ctx context.Context, parentID protocol.ConversationID, at protocol.EntryID, childID protocol.ConversationID) ([]forkCopy, error) {
	entry, err := t.store.ConversationEntry(ctx, parentID, at)
	if err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, fmt.Errorf("entry %d is not visible from conversation %d", at, parentID)
	}
	scope := record.DocumentScope{DocumentScopeFields: record.DocumentScopeFields{
		Kind:           "conversation",
		ConversationID: &parentID,
	}}
	var copies []forkCopy
	seen := map[string]bool{}
	if err := collectForkCopies(ctx, t.store, scope, storage.PointAt(entry.CommitSeq), "asOf", childID, seen, &copies); err != nil {
		return nil, err
	}
	if err := collectForkCopies(ctx, t.store, scope, storage.CurrentPoint(), "current", childID, seen, &copies); err != nil {
		return nil, err
	}
	return copies, nil
}

func collectForkCopies(
	ctx context.Context,
	store storage.Storage,
	scope record.DocumentScope,
	at storage.DocumentPoint,
	policy string,
	childID protocol.ConversationID,
	seen map[string]bool,
	copies *[]forkCopy,
) error {
	var cursor storage.Cursor
	for {
		page, err := store.ScanDocuments(ctx, storage.DocumentQuery{Scope: scope, At: at}, 256, cursor)
		if err != nil {
			return err
		}
		for _, source := range page.Items {
			if source.Scope.Kind != "conversation" || source.Fork != policy {
				continue
			}
			id, err := store.MintID(ctx)
			if err != nil {
				return err
			}
			rec := record.DocumentCreate{DocumentCreateFields: record.DocumentCreateFields{
				ID:      protocol.DocumentID(id),
				Kind:    source.Kind,
				Key:     source.Key,
				Scope:   record.DocumentScope{DocumentScopeFields: record.DocumentScopeFields{Kind: "conversation", ConversationID: &childID}},
				History: source.History,
				Fork:    source.Fork,
			}}
			address := source.Kind
			if source.Key != "" {
				address += "/" + source.Key
			}
			if seen[address] {
				return fmt.Errorf("fork selects multiple source documents for %s", address)
			}
			seen[address] = true
			*copies = append(*copies, forkCopy{
				record: rec,
				source: storage.DocumentCopySource{ID: source.ID, At: at},
			})
		}
		cursor = page.Next
		if len(cursor) == 0 {
			return nil
		}
	}
}
