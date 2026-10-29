package delta

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestParseOpShapes(t *testing.T) {
	cases := []struct {
		raw string
		ok  bool
	}{
		{`["r",{"a":1}]`, true},
		{`["s",["a"],1]`, true},
		{`["d",["a"]]`, true},
		{`["a",["a"],"x"]`, true},
		{`["t",["a"],1]`, true},
		{`["p",["a"],0,1,[2]]`, true},
		{`["m",["a"],[1,0]]`, true},
		{`["s"]`, false},
		{`["x",["a"],1]`, false},
		{`["s",["__proto__"],1]`, false},
		{`["p",["a"],-1,0,[]]`, false},
		{`["m",["a"],[1,1]]`, false},
	}
	for _, tc := range cases {
		var v any
		if err := json.Unmarshal([]byte(tc.raw), &v); err != nil {
			t.Fatal(err)
		}
		_, err := ParseOp(v)
		if (err == nil) != tc.ok {
			t.Fatalf("%s: err=%v want ok=%v", tc.raw, err, tc.ok)
		}
	}
}

func TestIsBase(t *testing.T) {
	if !IsBase([]Op{Replace{Value: 1.0}}) {
		t.Fatal("replace must be a base")
	}
	if IsBase([]Op{Set{Path: Path{Key("a")}, Value: 1.0}}) {
		t.Fatal("set must not be a base")
	}
	_, err := ParseOp(mustAny(t, `["x",1]`))
	if !errors.Is(err, ErrInvalidOp) {
		t.Fatal("unknown verb must wrap ErrInvalidOp")
	}
}

func mustAny(t *testing.T, raw string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatal(err)
	}
	return v
}
