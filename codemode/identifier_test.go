package codemode_test

import (
	"testing"

	"harness/codemode"
)

func TestToCodemodeIdentifier(t *testing.T) {
	cases := map[string]string{
		"my-tool":           "my_tool",
		"mcp__docs__search": "mcp__docs__search",
		"1abc":              "_abc",
		"":                  "_",
		"with space!":       "with_space_",
	}
	for in, want := range cases {
		if got := codemode.ToCodemodeIdentifier(in); got != want {
			t.Fatalf("ToCodemodeIdentifier(%q) = %q, want %q", in, got, want)
		}
	}
}
