package main

import (
	"encoding/json"
	"fmt"
	"os"

	"harness/delta"
)

func main() {
	raw, err := os.ReadFile("delta/testdata/delta-vectors.json")
	if err != nil {
		panic(err)
	}
	var vectors []struct {
		Before json.RawMessage `json:"before"`
		After  json.RawMessage `json:"after"`
	}
	if err := json.Unmarshal(raw, &vectors); err != nil {
		panic(err)
	}
	type emitted struct {
		Before any            `json:"before"`
		Wire   []delta.WireOp `json:"wire"`
		After  any            `json:"after"`
	}
	out := make([]emitted, 0, len(vectors))
	var enc delta.Encoder
	for _, vec := range vectors {
		var before, after any
		if err := json.Unmarshal(vec.Before, &before); err != nil {
			panic(err)
		}
		if err := json.Unmarshal(vec.After, &after); err != nil {
			panic(err)
		}
		ops := delta.DiffRevisions(before, after)
		out = append(out, emitted{Before: before, Wire: enc.Encode(ops), After: after})
	}
	data, err := json.Marshal(out)
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile("delta/testdata/emitted-delta.json", data, 0o644); err != nil {
		panic(err)
	}
	fmt.Printf("wrote %d emitted delta batches\n", len(out))
}
