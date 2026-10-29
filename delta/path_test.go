package delta

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestPathValidate(t *testing.T) {
	ok := Path{Key("a"), Index(0), Key("b")}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid path: %v", err)
	}
	for _, p := range []Path{
		{Key("__proto__")},
		{Key("constructor")},
		{Key("prototype")},
		{Index(-1)},
		{Index(1 << 53)},
	} {
		var unsafe *UnsafePathError
		if !errors.As(p.Validate(), &unsafe) {
			t.Fatalf("path %s: got %v, want UnsafePathError", p, p.Validate())
		}
	}
}

func TestPathJSON(t *testing.T) {
	p := Path{Key("a"), Index(2)}
	raw, err := json.Marshal(p)
	if err != nil || string(raw) != `["a",2]` {
		t.Fatalf("marshal %s %v", raw, err)
	}
	var back Path
	if err := json.Unmarshal(raw, &back); err != nil || back.String() != p.String() {
		t.Fatalf("unmarshal %v %v", back, err)
	}
	if string(mustJSON(t, Path(nil))) != "[]" {
		t.Fatal("nil path must marshal as []")
	}
	if err := json.Unmarshal([]byte(`[3.5]`), &back); err == nil {
		t.Fatal("non-integer segment must fail")
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
