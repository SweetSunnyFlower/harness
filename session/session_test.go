package session_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"harness/protocol"
	"harness/record"
	"harness/session"
	"harness/storage"
)

func TestCommitAndRollback(t *testing.T) {
	ctx := context.Background()
	s := session.New(storage.NewMemory())
	if _, err := s.Commit(ctx, func(tx *session.Tx) error {
		tx.Conversation(record.ConversationRecord{ConversationFields: record.ConversationFields{ID: 1}})
		tx.Entry(record.EntryRecord{EntryFields: record.EntryFields{ID: 2, ConversationID: 1, Kind: "a"}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Entry(ctx, 2); err != nil || got == nil {
		t.Fatalf("entry %v %v", got, err)
	}
	if _, err := s.Commit(ctx, func(tx *session.Tx) error {
		tx.Entry(record.EntryRecord{EntryFields: record.EntryFields{ID: 3, ConversationID: 1, Kind: "b"}})
		return errors.New("abort")
	}); err == nil {
		t.Fatal("expected callback error")
	}
	if got, err := s.Entry(ctx, 3); err != nil || got != nil {
		t.Fatalf("aborted write must not be visible: %v %v", got, err)
	}
}

func TestSerializedCommits(t *testing.T) {
	ctx := context.Background()
	s := session.New(storage.NewMemory())
	var mu sync.Mutex
	var seqs []protocol.Seq
	var wg sync.WaitGroup
	for i := 1; i <= 20; i++ {
		wg.Add(1)
		id := protocol.ConversationID(i)
		go func(id protocol.ConversationID) {
			defer wg.Done()
			seq, err := s.Commit(ctx, func(tx *session.Tx) error {
				tx.Conversation(record.ConversationRecord{ConversationFields: record.ConversationFields{ID: id}})
				return nil
			})
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			seqs = append(seqs, seq)
			mu.Unlock()
		}(id)
	}
	wg.Wait()
	if len(seqs) != 20 {
		t.Fatalf("seqs %d", len(seqs))
	}
	seen := map[protocol.Seq]bool{}
	for _, seq := range seqs {
		if seen[seq] {
			t.Fatalf("duplicate seq %d", seq)
		}
		seen[seq] = true
	}
}

func TestEditDocumentProducesDelta(t *testing.T) {
	ctx := context.Background()
	s := session.New(storage.NewMemory())
	conv := protocol.ConversationID(1)
	if _, err := s.Commit(ctx, func(tx *session.Tx) error {
		tx.Conversation(record.ConversationRecord{ConversationFields: record.ConversationFields{ID: 1}})
		tx.CreateDocument(
			record.DocumentCreate{DocumentCreateFields: record.DocumentCreateFields{
				ID: 10, Kind: "app.doc",
				Scope:   record.DocumentScope{DocumentScopeFields: record.DocumentScopeFields{Kind: "conversation", ConversationID: &conv}},
				History: "rewindable", Fork: "asOf",
			}},
			storage.DocumentContent{Version: 1, Kind: "base", Value: map[string]any{"v": 1.0}},
		)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit(ctx, func(tx *session.Tx) error {
		return tx.EditDocument(ctx, 10, func(current any) (any, error) {
			m := current.(map[string]any)
			m["v"] = 2.0
			m["extra"] = "x"
			return m, nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	doc, err := s.Document(ctx, 10, storage.CurrentPoint())
	if err != nil || doc == nil {
		t.Fatalf("doc %v %v", doc, err)
	}
	want := map[string]any{"v": 2.0, "extra": "x"}
	if !reflect.DeepEqual(doc.Value, want) {
		t.Fatalf("value %v want %v", doc.Value, want)
	}
}

func TestForkVisibility(t *testing.T) {
	ctx := context.Background()
	s := session.New(storage.NewMemory())
	if _, err := s.CreateConversation(ctx, record.ConversationRecord{ConversationFields: record.ConversationFields{ID: 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit(ctx, func(tx *session.Tx) error {
		tx.Entry(record.EntryRecord{EntryFields: record.EntryFields{ID: 5, ConversationID: 1, Kind: "root"}})
		tx.Entry(record.EntryRecord{EntryFields: record.EntryFields{ID: 10, ConversationID: 1, Kind: "m1"}})
		tx.Entry(record.EntryRecord{EntryFields: record.EntryFields{ID: 20, ConversationID: 1, Kind: "m2"}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Fork(ctx, 1, 10, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit(ctx, func(tx *session.Tx) error {
		tx.Entry(record.EntryRecord{EntryFields: record.EntryFields{ID: 30, ConversationID: 2, Kind: "child"}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	page, err := s.ScanEntries(ctx, storage.EntryQuery{ConversationID: 2, Order: storage.Ascending}, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	var ids []protocol.EntryID
	for _, e := range page.Items {
		ids = append(ids, e.ID)
	}
	want := []protocol.EntryID{5, 10, 30}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("fork visible %v want %v", ids, want)
	}
	if got, err := s.ConversationEntry(ctx, 2, 20); err != nil || got != nil {
		t.Fatalf("entry past fork cutoff must be invisible: %v %v", got, err)
	}
}
