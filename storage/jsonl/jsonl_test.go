package jsonl_test

import (
	"context"
	"testing"

	"harness/protocol"
	"harness/record"
	"harness/storage"
	"harness/storage/conformance"
	jsonlstorage "harness/storage/jsonl"
)

func TestJSONLConformance(t *testing.T) {
	conformance.Conformance(t, func(t *testing.T) storage.Storage {
		s, err := jsonlstorage.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close(nil) })
		return s
	})
}

func TestJSONLReopen(t *testing.T) {
	dir := t.TempDir()
	s, err := jsonlstorage.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := s.Commit(ctx, []storage.Write{
		storage.ConversationWrite{Value: record.ConversationRecord{ConversationFields: record.ConversationFields{ID: 1}}},
		storage.EntryWrite{Value: record.EntryRecord{EntryFields: record.EntryFields{ID: 2, ConversationID: 1, Kind: "a"}}},
	}, func(protocol.Seq) {}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close(ctx)

	s2, err := jsonlstorage.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s2.Conversation(ctx, 1)
	if err != nil || got == nil {
		t.Fatalf("reopen conversation %v %v", got, err)
	}
	e, err := s2.Entry(ctx, 2)
	if err != nil || e == nil {
		t.Fatalf("reopen entry %v %v", e, err)
	}
	_ = s2.Close(ctx)
}
