package storage

import (
	"context"
	"encoding/json"
	"errors"

	"harness/delta"
	"harness/protocol"
	"harness/record"
)

var ErrStorageRequest = errors.New("invalid storage request")

type Cursor = json.RawMessage

type ScanOrder string

const (
	Ascending  ScanOrder = "ascending"
	Descending ScanOrder = "descending"
)

type Page[T any] struct {
	Items []T
	Next  Cursor
}

type ConversationQuery struct {
	OwnerConversationID *protocol.ConversationID
	OwnerTaskID         *protocol.TaskID
	Order               ScanOrder
}

type EntryQuery struct {
	ConversationID protocol.ConversationID
	MinEntryID     *protocol.EntryID
	MaxEntryID     *protocol.EntryID
	Order          ScanOrder
}

type TaskQuery struct {
	ConversationID *protocol.ConversationID
	Kind           string
	Status         string
	AbortRequested *bool
	Background     *bool
	Order          ScanOrder
}

type SubmissionQuery struct {
	ConversationID *protocol.ConversationID
	Status         string
	Order          ScanOrder
}

type DocumentQuery struct {
	Scope record.DocumentScope
	At    DocumentPoint
	Kind  string
}

type DocumentPoint struct {
	seq     protocol.Seq
	current bool
}

func CurrentPoint() DocumentPoint            { return DocumentPoint{current: true} }
func PointAt(seq protocol.Seq) DocumentPoint { return DocumentPoint{seq: seq} }
func (p DocumentPoint) IsCurrent() bool      { return p.current }
func (p DocumentPoint) Seq() protocol.Seq    { return p.seq }

type DocumentAddress struct {
	Kind  string
	Scope record.DocumentScope
	Key   *string
}

type StoredDocument struct {
	Record          record.DocumentRecord
	Version         float64
	Value           any
	DeltasSinceBase int
}

type EntryAt struct {
	Entry     record.EntryRecord
	CommitSeq protocol.Seq
}

type DocumentCopySource struct {
	ID protocol.DocumentID
	At DocumentPoint
}

// Write is one atomic storage write. Each concrete type carries the exact
// record content the Session prepared.
type Write interface{ storageWrite() }

type ConversationWrite struct{ Value record.ConversationRecord }
type EntryWrite struct{ Value record.EntryRecord }
type TaskWrite struct{ Value record.TaskRecord }
type SubmissionWrite struct{ Value record.SubmissionRecord }

// DocumentCreateWrite writes a new document incarnation with a base content.
type DocumentCreateWrite struct {
	Record  record.DocumentCreate
	Content DocumentContent
}

// DocumentCopyWrite forks a conversation document by materializing the source
// at its point and storing it as a new independent base.
type DocumentCopyWrite struct {
	Record record.DocumentCreate
	Source DocumentCopySource
}

type DocumentChangeWrite struct {
	ID      protocol.DocumentID
	Content DocumentContent
}

type DocumentRetireWrite struct{ ID protocol.DocumentID }

func (ConversationWrite) storageWrite()   {}
func (EntryWrite) storageWrite()          {}
func (TaskWrite) storageWrite()           {}
func (SubmissionWrite) storageWrite()     {}
func (DocumentCreateWrite) storageWrite() {}
func (DocumentCopyWrite) storageWrite()   {}
func (DocumentChangeWrite) storageWrite() {}
func (DocumentRetireWrite) storageWrite() {}

// Storage persists one Session's records and document state. One Session owns
// one mutation line and serializes commits; backends must still make each
// admitted batch atomic. adopt must be called exactly once, synchronously,
// after the batch is durable, and before the next commit starts.
type Storage interface {
	Commit(ctx context.Context, writes []Write, adopt func(protocol.Seq)) (protocol.Seq, error)
	MintID(ctx context.Context) (protocol.RecordID, error)

	Conversation(ctx context.Context, id protocol.ConversationID) (*record.ConversationRecord, error)
	ScanConversations(ctx context.Context, q ConversationQuery, limit int, cursor Cursor) (Page[record.ConversationRecord], error)

	Entry(ctx context.Context, id protocol.EntryID) (*EntryAt, error)
	ConversationEntry(ctx context.Context, conversationID protocol.ConversationID, id protocol.EntryID) (*EntryAt, error)
	FindLatestHeadMarker(ctx context.Context, conversationID protocol.ConversationID, atOrBefore *protocol.EntryID) (*record.EntryRecord, error)
	ScanEntries(ctx context.Context, q EntryQuery, limit int, cursor Cursor) (Page[record.EntryRecord], error)

	Task(ctx context.Context, id protocol.TaskID) (*record.TaskRecord, error)
	ScanTasks(ctx context.Context, q TaskQuery, limit int, cursor Cursor) (Page[record.TaskRecord], error)

	Submission(ctx context.Context, id protocol.SubmissionID) (*record.SubmissionRecord, error)
	ScanSubmissions(ctx context.Context, q SubmissionQuery, limit int, cursor Cursor) (Page[record.SubmissionRecord], error)
	SubmissionByRequest(ctx context.Context, conversationID protocol.ConversationID, requestID string) (*record.SubmissionRecord, error)

	FindDocument(ctx context.Context, address DocumentAddress, at DocumentPoint) (*record.DocumentRecord, error)
	Document(ctx context.Context, id protocol.DocumentID, at DocumentPoint) (*StoredDocument, error)
	ScanDocuments(ctx context.Context, q DocumentQuery, limit int, cursor Cursor) (Page[record.DocumentRecord], error)

	Close(ctx context.Context) error
}

// DocumentContent is a base or a delta of a document incarnation. A delta
// carries decoded operations; a base carries the complete document root.
type DocumentContent struct {
	Version float64
	Kind    string // "base" | "delta"
	Value   any    // base root object
	Ops     []delta.Op
}
