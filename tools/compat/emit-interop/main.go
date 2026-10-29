package main

import (
	"context"
	"os"
	"path/filepath"

	"harness/delta"
	"harness/protocol"
	"harness/record"
	"harness/storage"
	"harness/storage/jsonl"
	"harness/storage/sqlite"
)

var ctx = context.Background()

func main() {
	dir := "build/interop"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		panic(err)
	}
	writes := sampleWrites()

	sqlPath := filepath.Join(dir, "storage.sqlite")
	os.Remove(sqlPath)
	sq, err := sqlite.Open(sqlPath)
	if err != nil {
		panic(err)
	}
	commit(sq, writes)
	commit(sq, []storage.Write{storage.DocumentChangeWrite{
		ID:      10,
		Content: storage.DocumentContent{Version: 1, Kind: "delta", Ops: []delta.Op{delta.Set{Path: delta.Path{delta.Key("v")}, Value: 2.0}}},
	}})
	if err := sq.Close(ctx); err != nil {
		panic(err)
	}

	jsonlDir := filepath.Join(dir, "jsonl")
	os.RemoveAll(jsonlDir)
	jl, err := jsonl.Open(jsonlDir)
	if err != nil {
		panic(err)
	}
	commit(jl, writes)
	commit(jl, []storage.Write{storage.DocumentChangeWrite{
		ID:      10,
		Content: storage.DocumentContent{Version: 1, Kind: "delta", Ops: []delta.Op{delta.Set{Path: delta.Path{delta.Key("v")}, Value: 2.0}}},
	}})
	if err := jl.Close(ctx); err != nil {
		panic(err)
	}
}

func commit(s storage.Storage, writes []storage.Write) {
	if _, err := s.Commit(ctx, writes, func(protocol.Seq) {}); err != nil {
		panic(err)
	}
}

func sampleWrites() []storage.Write {
	conv := protocol.ConversationID(1)
	return []storage.Write{
		storage.ConversationWrite{Value: record.ConversationRecord{ConversationFields: record.ConversationFields{ID: 1}}},
		storage.EntryWrite{Value: record.EntryRecord{EntryFields: record.EntryFields{ID: 2, ConversationID: 1, Kind: "app.note"}}},
		storage.TaskWrite{Value: record.TaskRecord{TaskFields: record.TaskFields{
			ID: 3, ConversationID: 1, Kind: "app.task", Version: 1, Input: []byte("null"),
			Background: false, AbortRequested: false,
			State: record.TaskState{TaskStateFields: record.TaskStateFields{Status: "pending", Checkpoint: []byte(`{"phase":"run"}`)}},
		}}},
		storage.DocumentCreateWrite{
			Record: record.DocumentCreate{DocumentCreateFields: record.DocumentCreateFields{
				ID:      10,
				Kind:    "app.doc",
				Scope:   record.DocumentScope{DocumentScopeFields: record.DocumentScopeFields{Kind: "conversation", ConversationID: &conv}},
				History: "rewindable",
				Fork:    "asOf",
			}},
			Content: storage.DocumentContent{Version: 1, Kind: "base", Value: map[string]any{"v": 1.0}},
		},
	}
}
