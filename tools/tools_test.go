package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"harness/harness"
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

func TestSearch(t *testing.T) {
	tm := map[string]harness.ToolDef{
		"read":       {Name: "read", Description: "Read a file as text."},
		"write":      {Name: "write", Description: "Write text to a file."},
		"bash":       {Name: "bash", Description: "Run a shell command."},
		"read_bytes": {Name: "read_bytes", Description: "Read raw bytes from a file."},
	}
	got := tools.Search(tm, "read", 0)
	if len(got) < 2 || got[0].Name != "read" {
		t.Fatalf("search %#v", got)
	}
	if got[0].Name == "bash" {
		t.Fatal("bash should not rank for read")
	}
}
