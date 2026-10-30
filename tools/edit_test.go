package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyEditsToNormalizedContent(t *testing.T) {
	base, next, err := ApplyEditsToNormalizedContent("a\nb\nc\n", []Edit{{OldText: "b", NewText: "B"}}, "f")
	if err != nil {
		t.Fatal(err)
	}
	if base != "a\nb\nc\n" || next != "a\nB\nc\n" {
		t.Fatalf("unexpected: base=%q next=%q", base, next)
	}
}

func TestApplyEditsOverlap(t *testing.T) {
	_, _, err := ApplyEditsToNormalizedContent("hello\n", []Edit{
		{OldText: "hello", NewText: "x"},
		{OldText: "ell", NewText: "y"},
	}, "f")
	if err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("expected overlap error, got %v", err)
	}
}

func TestApplyEditsNotFound(t *testing.T) {
	_, _, err := ApplyEditsToNormalizedContent("hello\n", []Edit{{OldText: "missing", NewText: "x"}}, "f")
	if err == nil || !strings.Contains(err.Error(), "Could not find") {
		t.Fatalf("expected not found error, got %v", err)
	}
}

func TestApplyEditsFuzzyMatch(t *testing.T) {
	_, next, err := ApplyEditsToNormalizedContent("foo  \nbar\n", []Edit{{OldText: "foo", NewText: "FOO"}}, "f")
	if err != nil {
		t.Fatal(err)
	}
	if next != "FOO  \nbar\n" {
		t.Fatalf("unexpected next: %q", next)
	}
}

func TestEditToolCRLF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("a\r\nb\r\nc\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := EditTool()
	_, err := tool.Execute(context.Background(), map[string]any{
		"path": path,
		"edits": []any{
			map[string]any{"oldText": "b", "newText": "B"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "a\r\nB\r\nc\r\n" {
		t.Fatalf("unexpected file content: %q", string(raw))
	}
}
