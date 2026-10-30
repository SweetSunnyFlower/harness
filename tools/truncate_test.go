package tools_test

import (
	"strings"
	"testing"

	"harness/tools"
)

func TestTruncateHeadLines(t *testing.T) {
	content := "a\nb\nc\n"
	got := tools.TruncateHead(content, 2, 1024)
	if !got.Truncated || got.TruncatedBy != "lines" || got.Content != "a\nb" {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestTruncateHeadBytes(t *testing.T) {
	content := "aaaa\nbbbb\n"
	got := tools.TruncateHead(content, 100, 6)
	if !got.Truncated || got.TruncatedBy != "bytes" || got.Content != "aaaa" {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestReadToolOffsetLimit(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/lines.txt"
	write := tools.WriteTool()
	if _, err := write.Execute(t.Context(), map[string]any{"path": path, "content": "l1\nl2\nl3\nl4\n"}); err != nil {
		t.Fatal(err)
	}
	read := tools.ReadTool()
	got, err := read.Execute(t.Context(), map[string]any{"path": path, "offset": 2.0, "limit": 2.0})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimRight(got, "\n") != "l2\nl3" {
		t.Fatalf("unexpected: %q", got)
	}
}
