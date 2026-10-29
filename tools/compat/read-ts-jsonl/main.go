package main

import (
	"context"
	"fmt"
	"reflect"

	"harness/storage"
	"harness/storage/jsonl"
)

func main() {
	ctx := context.Background()
	s, err := jsonl.Open("build/interop/ts-jsonl")
	if err != nil {
		panic(err)
	}
	defer s.Close(ctx)
	conv, err := s.Conversation(ctx, 1)
	if err != nil || conv == nil {
		panic(fmt.Sprintf("conversation %v %v", conv, err))
	}
	entry, err := s.Entry(ctx, 2)
	if err != nil || entry == nil || entry.Entry.Kind != "app.note" {
		panic(fmt.Sprintf("entry %v %v", entry, err))
	}
	doc, err := s.Document(ctx, 10, storage.CurrentPoint())
	if err != nil || doc == nil || !reflect.DeepEqual(doc.Value, map[string]any{"v": 2.0}) {
		panic(fmt.Sprintf("document %v %v", doc, err))
	}
	fmt.Println("PASS Go reads TS-written JSONL")
}
