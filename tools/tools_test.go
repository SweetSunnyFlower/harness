package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"harness/tools"
)

func TestReadWriteBash(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	write := tools.WriteTool()
	if _, err := write.Execute(ctx, map[string]any{"path": path, "content": "hello"}); err != nil {
		t.Fatal(err)
	}
	read := tools.ReadTool()
	got, err := read.Execute(ctx, map[string]any{"path": path})
	if err != nil || got != "hello" {
		t.Fatalf("read %q %v", got, err)
	}
	bash := tools.BashTool()
	out, err := bash.Execute(ctx, map[string]any{"command": "echo hi"})
	if err != nil || strings.TrimSpace(out) != "hi" {
		t.Fatalf("bash %q %v", out, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}
