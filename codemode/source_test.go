package codemode_test

import (
	"testing"

	"harness/codemode"
)

func TestParseCodemodeSource(t *testing.T) {
	parsed, err := codemode.ParseCodemodeSource(`// @options: {"timeout_ms": 5000}
text("hi")`)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Options.TimeoutMs != 5000 {
		t.Fatalf("unexpected options: %+v", parsed.Options)
	}
	if parsed.Code != "\ntext(\"hi\")" {
		t.Fatalf("unexpected code: %q", parsed.Code)
	}
}

func TestParseCodemodeSourceInvalid(t *testing.T) {
	if _, err := codemode.ParseCodemodeSource(`// @options: {"bogus": 1}
x`); err == nil {
		t.Fatal("expected error")
	}
}
