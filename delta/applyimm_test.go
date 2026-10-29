package delta

import "testing"

func TestApplyImmutableDoesNotMutateInput(t *testing.T) {
	base := map[string]any{"xs": []any{1.0}}
	next, err := ApplyImmutable(base, []Op{Set{Path: Path{Key("xs"), Index(0)}, Value: 9.0}})
	if err != nil {
		t.Fatal(err)
	}
	if base["xs"].([]any)[0] != 1.0 {
		t.Fatal("input mutated")
	}
	if next.(map[string]any)["xs"].([]any)[0] != 9.0 {
		t.Fatal("result wrong")
	}
}

func TestApplyImmutableBatches(t *testing.T) {
	base := map[string]any{"n": 0.0}
	batches := [][]Op{
		{Set{Path: Path{Key("n")}, Value: 1.0}},
		{Set{Path: Path{Key("n")}, Value: 2.0}},
	}
	got, err := ApplyImmutableBatches(base, batches)
	if err != nil {
		t.Fatal(err)
	}
	if got.(map[string]any)["n"] != 2.0 || base["n"] != 0.0 {
		t.Fatal("batch replay wrong")
	}
}
