package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"harness/harness"
)

// ReadTool reads a file as text.
func ReadTool() harness.ToolDef {
	return harness.ToolDef{
		Name:        "read",
		Description: "Read a file as text.",
		Replay:      "safe",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			path, _ := args["path"].(string)
			if path == "" {
				return "", fmt.Errorf("path required")
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
	}
}

// WriteTool writes text to a file.
func WriteTool() harness.ToolDef {
	return harness.ToolDef{
		Name:        "write",
		Description: "Write text to a file.",
		Replay:      "unsafe",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			path, _ := args["path"].(string)
			content, _ := args["content"].(string)
			if path == "" {
				return "", fmt.Errorf("path required")
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return "", err
			}
			return "ok", nil
		},
	}
}

// BashTool runs a command through the shell.
func BashTool() harness.ToolDef {
	return harness.ToolDef{
		Name:        "bash",
		Description: "Run a shell command.",
		Replay:      "unsafe",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			command, _ := args["command"].(string)
			if command == "" {
				return "", fmt.Errorf("command required")
			}
			cmd := exec.CommandContext(ctx, "sh", "-c", command)
			var out strings.Builder
			cmd.Stdout = &out
			cmd.Stderr = &out
			if err := cmd.Run(); err != nil {
				return out.String(), err
			}
			return out.String(), nil
		},
	}
}
