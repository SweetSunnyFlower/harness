package delta

import "testing"

func TestJSONEqualAndKeyOrder(t *testing.T) {
	a := map[string]any{"b": 1.0, "10": "x", "2": "y"}
	b := map[string]any{"2": "y", "10": "x", "b": 1.0}
	if !jsonEqual(a, b) {
		t.Fatal("equal objects with different insertion order")
	}
	if jsonEqual(a, map[string]any{"b": 2.0, "10": "x", "2": "y"}) {
		t.Fatal("differing scalar must not be equal")
	}
	got := keyOrder(a)
	want := []string{"2", "10", "b"}
	if len(got) != len(want) {
		t.Fatalf("key order %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("key order %v, want %v", got, want)
		}
	}
}

func TestDeepCopyDoesNotAlias(t *testing.T) {
	in := map[string]any{"xs": []any{1.0}}
	out := deepCopy(in).(map[string]any)
	out["xs"].([]any)[0] = 9.0
	if in["xs"].([]any)[0] != 1.0 {
		t.Fatal("deep copy aliases input")
	}
}
