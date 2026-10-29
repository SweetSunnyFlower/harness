package session

import (
	"context"
	"sync"

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
	tx := &Tx{}
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

// Tx accumulates writes for one atomic commit.
type Tx struct {
	writes []storage.Write
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
