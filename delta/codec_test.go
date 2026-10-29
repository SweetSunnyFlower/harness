package delta

import (
	"encoding/json"
	"testing"
)

func TestCodecRoundTripAcrossBatches(t *testing.T) {
	batches := [][]Op{
		{Replace{Value: map[string]any{"a": 1.0}}},
		{Set{Path: Path{Key("a")}, Value: 2.0}},
		{Set{Path: Path{Key("a")}, Value: 3.0}, Delete{Path: Path{Key("a")}}},
	}
	var enc Encoder
	var dec Decoder
	for _, batch := range batches {
		wire := enc.Encode(batch)
		got, err := dec.Decode(wire)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(batch) {
			t.Fatalf("len %d want %d", len(got), len(batch))
		}
		for i := range batch {
			if !jsonEqual(got[i], batch[i]) {
				t.Fatalf("op %d: %#v want %#v", i, got[i], batch[i])
			}
		}
	}
}

func TestCodecResetsOnBase(t *testing.T) {
	var enc Encoder
	enc.Encode([]Op{Set{Path: Path{Key("a")}, Value: 1.0}})
	wire := enc.Encode([]Op{Replace{Value: 1.0}, Set{Path: Path{Key("a")}, Value: 2.0}})
	var dec Decoder
	ops, err := dec.Decode(wire)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 2 || ops[1].(Set).Path.String() != `["a"]` {
		t.Fatalf("reset failed: %#v", ops)
	}
}

func TestWireJSONRoundTrip(t *testing.T) {
	op := WireSplice{Ref: Path{Key("xs")}, Index: 1, Remove: 1, Items: []any{9.0}}
	raw, err := json.Marshal(op)
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseWireOp(v)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.(WireSplice).Index != 1 {
		t.Fatalf("round trip: %#v", parsed)
	}
}

func TestCodecShortFormRequiresPrevious(t *testing.T) {
	var dec Decoder
	if _, err := dec.Decode([]WireOp{WireSet{Ref: nil, Value: 1.0}}); err == nil {
		t.Fatal("short form without previous path must error")
	}
}
