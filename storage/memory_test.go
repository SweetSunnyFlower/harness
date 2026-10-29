package storage_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"harness/delta"
	"harness/protocol"
	"harness/record"
	"harness/storage"
)

var ctx = context.Background()

func conv(id protocol.ConversationID) storage.ConversationWrite {
	return storage.ConversationWrite{Value: record.ConversationRecord{
		ConversationFields: record.ConversationFields{ID: id},
	}}
}

func entry(id protocol.EntryID, conversation protocol.ConversationID, kind string) storage.EntryWrite {
	return storage.EntryWrite{Value: record.EntryRecord{
		EntryFields: record.EntryFields{ID: id, ConversationID: conversation, Kind: kind},
	}}
}

func task(id protocol.TaskID, conversation protocol.ConversationID, kind string, status string) storage.TaskWrite {
	return storage.TaskWrite{Value: record.TaskRecord{
		TaskFields: record.TaskFields{
			ID:             id,
			ConversationID: conversation,
			Kind:           kind,
			Version:        1,
			Input:          []byte("null"),
			Background:     false,
			AbortRequested: false,
			State: record.TaskState{
				TaskStateFields: record.TaskStateFields{Status: status, Checkpoint: []byte(`{"phase":"run"}`)},
			},
		},
	}}
}

func submission(id protocol.SubmissionID, conversation protocol.ConversationID, requestID, status string) storage.SubmissionWrite {
	return storage.SubmissionWrite{Value: record.SubmissionRecord{
		SubmissionFields: record.SubmissionFields{
			ID:             id,
			ConversationID: conversation,
			RequestID:      requestID,
			Type:           "input",
			Status:         status,
		},
	}}
}

func commitAll(t *testing.T, s storage.Storage, writes []storage.Write) protocol.Seq {
	t.Helper()
	seq, err := s.Commit(ctx, writes, func(protocol.Seq) {})
	if err != nil {
		t.Fatal(err)
	}
	return seq
}

func TestCommitMintIDAndNamespace(t *testing.T) {
	s := storage.NewMemory()
	id1, err := s.MintID(ctx)
	if err != nil || id1 != 2 {
		t.Fatalf("first mint %v %v", id1, err)
	}
	commitAll(t, s, []storage.Write{
		conv(1),
		entry(3, 1, "a"),
		task(5, 1, "t", "pending"),
	})
	id2, err := s.MintID(ctx)
	if err != nil || id2 != 6 {
		t.Fatalf("next mint %v %v", id2, err)
	}
	if _, err := s.Commit(ctx, []storage.Write{entry(1, 1, "clash")}, func(protocol.Seq) {}); err == nil {
		t.Fatal("ID reused across record kinds must fail")
	}
	if _, err := s.Commit(ctx, []storage.Write{conv(1)}, func(protocol.Seq) {}); err == nil {
		t.Fatal("duplicate conversation id must fail")
	}
}

func TestConversationScan(t *testing.T) {
	s := storage.NewMemory()
	owner := protocol.ConversationID(1)
	child := protocol.ConversationID(2)
	commitAll(t, s, []storage.Write{
		storage.ConversationWrite{Value: record.ConversationRecord{ConversationFields: record.ConversationFields{ID: owner}}},
		storage.ConversationWrite{Value: record.ConversationRecord{ConversationFields: record.ConversationFields{
			ID:    child,
			Owner: &record.ConversationOwner{ConversationID: owner, TaskID: 9},
		}}},
		conv(3),
	})
	page, err := s.ScanConversations(ctx, storage.ConversationQuery{Order: storage.Ascending}, 2, nil)
	if err != nil || len(page.Items) != 2 || page.Next == nil {
		t.Fatalf("first page %v %v", page, err)
	}
	page2, err := s.ScanConversations(ctx, storage.ConversationQuery{}, 2, page.Next)
	if err != nil || len(page2.Items) != 1 {
		t.Fatalf("second page %v %v", page2, err)
	}
	byOwner, err := s.ScanConversations(ctx, storage.ConversationQuery{OwnerConversationID: &owner}, 10, nil)
	if err != nil || len(byOwner.Items) != 1 || byOwner.Items[0].ID != child {
		t.Fatalf("owner scan %v %v", byOwner, err)
	}
}

func TestEntryVisibilityAndHeadMarker(t *testing.T) {
	s := storage.NewMemory()
	commitAll(t, s, []storage.Write{
		conv(1),
		storage.EntryWrite{Value: record.EntryRecord{EntryFields: record.EntryFields{ID: 5, ConversationID: 1, Kind: "root", Head: ptr(protocol.EntryID(5))}}},
		storage.EntryWrite{Value: record.EntryRecord{EntryFields: record.EntryFields{ID: 10, ConversationID: 1, Kind: "m1"}}},
		storage.EntryWrite{Value: record.EntryRecord{EntryFields: record.EntryFields{ID: 20, ConversationID: 1, Kind: "m2"}}},
		storage.ConversationWrite{Value: record.ConversationRecord{ConversationFields: record.ConversationFields{
			ID:     2,
			Parent: &record.ConversationParent{ConversationID: 1, At: 10},
		}}},
		entry(30, 2, "child1"),
		entry(40, 2, "child2"),
	})
	desc, err := s.ScanEntries(ctx, storage.EntryQuery{ConversationID: 2}, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	gotIDs := make([]protocol.EntryID, len(desc.Items))
	for i, e := range desc.Items {
		gotIDs[i] = e.ID
	}
	if !reflect.DeepEqual(gotIDs, []protocol.EntryID{40, 30, 10, 5}) {
		t.Fatalf("descending visible %v", gotIDs)
	}
	asc, err := s.ScanEntries(ctx, storage.EntryQuery{ConversationID: 2, Order: storage.Ascending}, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	ascIDs := make([]protocol.EntryID, len(asc.Items))
	for i, e := range asc.Items {
		ascIDs[i] = e.ID
	}
	if !reflect.DeepEqual(ascIDs, []protocol.EntryID{5, 10, 30, 40}) {
		t.Fatalf("ascending visible %v", ascIDs)
	}
	if got, err := s.ConversationEntry(ctx, 2, 20); err != nil || got != nil {
		t.Fatalf("entry past parent cutoff must be invisible: %v %v", got, err)
	}
	if got, err := s.ConversationEntry(ctx, 2, 10); err != nil || got == nil || got.Entry.ID != 10 {
		t.Fatalf("parent entry at cutoff must be visible: %v %v", got, err)
	}

	// Parent-walk head marker: root entry 5 carries head 5.
	marker, err := s.FindLatestHeadMarker(ctx, 2, nil)
	if err != nil || marker == nil || marker.ID != 5 || marker.Head == nil || *marker.Head != 5 {
		t.Fatalf("parent head marker %v %v", marker, err)
	}
	// Own head marker wins over the parent walk.
	commitAll(t, s, []storage.Write{
		storage.EntryWrite{Value: record.EntryRecord{EntryFields: record.EntryFields{ID: 41, ConversationID: 2, Kind: "child3", Head: ptr(protocol.EntryID(30))}}},
	})
	marker, err = s.FindLatestHeadMarker(ctx, 2, nil)
	if err != nil || marker == nil || marker.ID != 41 || *marker.Head != 30 {
		t.Fatalf("own head marker %v %v", marker, err)
	}
}

func TestTaskAndSubmissionScans(t *testing.T) {
	s := storage.NewMemory()
	commitAll(t, s, []storage.Write{
		conv(1),
		task(2, 1, "app.a", "pending"),
		task(3, 1, "app.b", "running"),
		submission(4, 1, "req-1", "queued"),
		submission(5, 1, "", "done"),
	})
	pending, err := s.ScanTasks(ctx, storage.TaskQuery{Status: "pending"}, 10, nil)
	if err != nil || len(pending.Items) != 1 || pending.Items[0].ID != 2 {
		t.Fatalf("task status scan %v %v", pending, err)
	}
	byReq, err := s.SubmissionByRequest(ctx, 1, "req-1")
	if err != nil || byReq == nil || byReq.ID != 4 {
		t.Fatalf("submission by request %v %v", byReq, err)
	}
	// Status transition updates the request index and status buckets.
	commitAll(t, s, []storage.Write{
		submission(4, 1, "req-1", "done"),
	})
	done, err := s.ScanSubmissions(ctx, storage.SubmissionQuery{Status: "done"}, 10, nil)
	if err != nil || len(done.Items) != 2 {
		t.Fatalf("submission status scan %v %v", done, err)
	}
}

func conversationDoc(id protocol.DocumentID, kind string, history string, fork string) storage.DocumentCreateWrite {
	conv := protocol.ConversationID(2)
	return storage.DocumentCreateWrite{
		Record: record.DocumentCreate{DocumentCreateFields: record.DocumentCreateFields{
			ID:      id,
			Kind:    kind,
			Scope:   record.DocumentScope{DocumentScopeFields: record.DocumentScopeFields{Kind: "conversation", ConversationID: &conv}},
			History: history,
			Fork:    fork,
		}},
		Content: storage.DocumentContent{Version: 1, Kind: "base", Value: map[string]any{"v": 1.0}},
	}
}

func TestDocumentLifecycleAndMaterialize(t *testing.T) {
	s := storage.NewMemory()
	baseSeq := commitAll(t, s, []storage.Write{conv(1), conversationDoc(10, "app.doc", "rewindable", "asOf")})
	commitAll(t, s, []storage.Write{storage.DocumentChangeWrite{
		ID:      10,
		Content: storage.DocumentContent{Version: 1, Kind: "delta", Ops: []delta.Op{delta.Set{Path: delta.Path{delta.Key("v")}, Value: 2.0}}},
	}})
	current, err := s.Document(ctx, 10, storage.CurrentPoint())
	if err != nil || current == nil || !reflect.DeepEqual(current.Value, map[string]any{"v": 2.0}) {
		t.Fatalf("current materialize %v %v", current, err)
	}
	atBase, err := s.Document(ctx, 10, storage.PointAt(baseSeq))
	if err != nil || atBase == nil || !reflect.DeepEqual(atBase.Value, map[string]any{"v": 1.0}) {
		t.Fatalf("historical materialize %v %v", atBase, err)
	}
	commitAll(t, s, []storage.Write{storage.DocumentRetireWrite{ID: 10}})
	if got, err := s.Document(ctx, 10, storage.CurrentPoint()); err != nil || got != nil {
		t.Fatalf("retired current must be absent: %v %v", got, err)
	}
	if got, err := s.Document(ctx, 10, storage.PointAt(baseSeq)); err != nil || got == nil {
		t.Fatalf("retired historical must remain: %v %v", got, err)
	}
}

func TestDocumentCopyAndCurrentOnly(t *testing.T) {
	s := storage.NewMemory()
	baseSeq := commitAll(t, s, []storage.Write{conv(1), conversationDoc(10, "app.doc", "rewindable", "asOf")})
	commitAll(t, s, []storage.Write{storage.DocumentChangeWrite{
		ID:      10,
		Content: storage.DocumentContent{Version: 1, Kind: "delta", Ops: []delta.Op{delta.Set{Path: delta.Path{delta.Key("v")}, Value: 2.0}}},
	}})
	conv := protocol.ConversationID(1)
	copySeq := commitAll(t, s, []storage.Write{storage.DocumentCopyWrite{
		Record: record.DocumentCreate{DocumentCreateFields: record.DocumentCreateFields{
			ID:      11,
			Kind:    "app.doc",
			Scope:   record.DocumentScope{DocumentScopeFields: record.DocumentScopeFields{Kind: "conversation", ConversationID: &conv}},
			History: "rewindable",
			Fork:    "asOf",
		}},
		Source: storage.DocumentCopySource{ID: 10, At: storage.PointAt(baseSeq)},
	}})
	copied, err := s.Document(ctx, 11, storage.CurrentPoint())
	if err != nil || copied == nil || !reflect.DeepEqual(copied.Value, map[string]any{"v": 1.0}) {
		t.Fatalf("copied value %v %v", copied, err)
	}
	if copySeq == baseSeq {
		t.Fatal("copy must be its own commit")
	}

	// current-only documents reject historical reads.
	only := commitAll(t, s, []storage.Write{conversationDoc(12, "app.latest", "latest", "current")})
	if _, err := s.Document(ctx, 12, storage.PointAt(only)); !errors.Is(err, storage.ErrStorageRequest) {
		t.Fatalf("current-only historical read: %v", err)
	}
}

func TestReadIsolation(t *testing.T) {
	s := storage.NewMemory()
	commitAll(t, s, []storage.Write{conv(1), task(2, 1, "app.a", "pending")})
	got, err := s.Task(ctx, 2)
	if err != nil || got == nil {
		t.Fatal(err)
	}
	got.Kind = "mutated"
	again, err := s.Task(ctx, 2)
	if err != nil || again == nil || again.Kind != "app.a" {
		t.Fatalf("read must be detached: %v", again)
	}
}

func ptr[T any](v T) *T { return &v }
