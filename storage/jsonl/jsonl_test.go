package jsonl_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"harness/delta"
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

func TestJSONLReopenFSync(t *testing.T) {
	dir := t.TempDir()
	s, err := jsonlstorage.OpenWithOptions(dir, jsonlstorage.Options{FSync: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := s.Commit(ctx, []storage.Write{
		storage.ConversationWrite{Value: record.ConversationRecord{ConversationFields: record.ConversationFields{ID: 1}}},
	}, func(protocol.Seq) {}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close(ctx)
	s2, err := jsonlstorage.OpenWithOptions(dir, jsonlstorage.Options{FSync: true})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s2.Conversation(ctx, 1)
	if err != nil || got == nil {
		t.Fatalf("fsync reopen %v %v", got, err)
	}
	_ = s2.Close(ctx)
}

func TestJSONLReclaimRetiredCurrentOnlyDocument(t *testing.T) {
	dir := t.TempDir()
	s, err := jsonlstorage.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	conv := protocol.ConversationID(1)
	create := storage.DocumentCreateWrite{
		Record: record.DocumentCreate{DocumentCreateFields: record.DocumentCreateFields{
			ID:      10,
			Kind:    "app.latest",
			Scope:   record.DocumentScope{DocumentScopeFields: record.DocumentScopeFields{Kind: "conversation", ConversationID: &conv}},
			History: "latest",
			Fork:    "current",
		}},
		Content: storage.DocumentContent{Version: 1, Kind: "base", Value: map[string]any{"v": 1.0}},
	}
	if _, err := s.Commit(ctx, []storage.Write{storage.ConversationWrite{Value: record.ConversationRecord{ConversationFields: record.ConversationFields{ID: 1}}}, create}, func(protocol.Seq) {}); err != nil {
		t.Fatal(err)
	}
	change := storage.DocumentChangeWrite{ID: 10, Content: storage.DocumentContent{Version: 1, Kind: "delta", Ops: []delta.Op{delta.Set{Path: delta.Path{delta.Key("v")}, Value: 2.0}}}}
	if _, err := s.Commit(ctx, []storage.Write{change}, func(protocol.Seq) {}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit(ctx, []storage.Write{storage.DocumentRetireWrite{ID: 10}}, func(protocol.Seq) {}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "doc-10.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("reclaimed sidecar should be removed: %v", err)
	}
	_ = s.Close(ctx)

	s2, err := jsonlstorage.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s2.Document(ctx, 10, storage.CurrentPoint())
	if err != nil || got != nil {
		t.Fatalf("retired doc must be absent after reopen: %v %v", got, err)
	}
	_ = s2.Close(ctx)
}
