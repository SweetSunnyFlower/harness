package delta

import (
	"encoding/json"
	"os"
	"testing"
)

func TestDeltaVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/delta-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Before json.RawMessage `json:"before"`
		After  json.RawMessage `json:"after"`
	}
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	for i, vec := range vectors {
		before, err := decodeJSON(vec.Before)
		if err != nil {
			t.Fatal(err)
		}
		after, err := decodeJSON(vec.After)
		if err != nil {
			t.Fatal(err)
		}
		ops := DiffRevisions(before, after)
		got, err := Apply(before, ops)
		if err != nil {
			t.Fatalf("vector %d: %v", i, err)
		}
		if !jsonEqual(got, after) {
			t.Fatalf("vector %d: got %v want %v", i, got, after)
		}
	}
}

func decodeJSON(raw json.RawMessage) (any, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return v, nil
}
