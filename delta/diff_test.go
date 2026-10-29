package delta

import (
	"encoding/json"
	"testing"
)

func TestDiffRevisionsRoundTrips(t *testing.T) {
	cases := [][2]string{
		{`{"a":1}`, `{"a":1,"b":2}`},
		{`{"a":1,"b":2}`, `{"a":1}`},
		{`{"s":"abc"}`, `{"s":"abcdef"}`},
		{`{"s":"abc"}`, `{"s":"xyz"}`},
		{`{"xs":[1,2]}`, `{"xs":[1,3]}`},
		{`1`, `2`},
		{`{"n":1}`, `{"n":1}`},
	}
	for _, tc := range cases {
		var before, after any
		if err := json.Unmarshal([]byte(tc[0]), &before); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(tc[1]), &after); err != nil {
			t.Fatal(err)
		}
		ops := DiffRevisions(before, after)
		got, err := Apply(before, ops)
		if err != nil {
			t.Fatalf("%s -> %s: %v", tc[0], tc[1], err)
		}
		if !jsonEqual(got, after) {
			t.Fatalf("%s -> %s: got %v", tc[0], tc[1], got)
		}
	}
}
