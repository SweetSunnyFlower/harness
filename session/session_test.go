package session_test

import (
	"context"
	"errors"
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
