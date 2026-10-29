package delta

import (
	"encoding/json"
	"errors"
	"testing"
)

func tree(t *testing.T, raw string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func ops(t *testing.T, raw string) []Op {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatal(err)
	}
	batch, ok := v.([]any)
	if !ok {
		t.Fatal("not a batch")
	}
	out := make([]Op, 0, len(batch))
	for _, item := range batch {
		op, err := ParseOp(item)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, op)
	}
	return out
}

func TestApplyVerbs(t *testing.T) {
	root := tree(t, `{"text":"abc","xs":[1,2,3],"obj":{"a":1},"gone":true}`)
	got, err := Apply(root, ops(t, `[
		["a",["text"],"d"],
		["t",["text"],2],
		["p",["xs"],1,1,[9]],
		["s",["obj","b"],2],
		["d",["gone"]]
	]`))
	if err != nil {
		t.Fatal(err)
	}
	want := tree(t, `{"text":"cd","xs":[1,9,3],"obj":{"a":1,"b":2}}`)
	if !jsonEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestApplyTruncateUTF16(t *testing.T) {
	root := tree(t, `{"s":"\uD83D\uDE00x"}`)
	got, err := Apply(root, []Op{Truncate{Path: Path{Key("s")}, Count: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if got.(map[string]any)["s"] != "\uFFFDx" {
		t.Fatalf("surrogate split: %q", got.(map[string]any)["s"])
	}
}

func TestApplyErrors(t *testing.T) {
	var unsafe *UnsafePathError
	var pathErr *PathError
	if _, err := Apply(tree(t, `{"a":1}`), []Op{Set{Path: Path{Key("__proto__")}, Value: 1.0}}); !errors.As(err, &unsafe) {
		t.Fatalf("reserved: %v", err)
	}
	if _, err := Apply(tree(t, `{"xs":[1]}`), []Op{Set{Path: Path{Key("xs"), Index(5)}, Value: 1.0}}); !errors.As(err, &unsafe) {
		t.Fatalf("gap: %v", err)
	}
	if _, err := Apply(tree(t, `{}`), []Op{Set{Path: Path{Key("missing"), Key("child")}, Value: 1.0}}); !errors.As(err, &pathErr) {
		t.Fatalf("want PathError: %v", err)
	}
}

func TestSpliceClamps(t *testing.T) {
	got, err := Apply(tree(t, `[1,2,3]`), []Op{Splice{Path: Path{}, Index: 2, Remove: 10, Items: []any{4.0}}})
	if err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(got, []any{1.0, 2.0, 4.0}) {
		t.Fatalf("clamped splice: %v", got)
	}
}
