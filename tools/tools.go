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
		Description: "Read the contents of a text file. Use offset/limit for large files.",
		Replay:      "safe",
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			path, _ := args["path"].(string)
			if path == "" {
				return "", fmt.Errorf("path required")
			}
			if mime := DetectImageMimeTypeOf(path); mime != "" {
				return "", fmt.Errorf("%s is an image (%s); reading images is not supported", path, mime)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return "", err
			}
			text := string(b)
			offset := intArg(args["offset"], 0)
			limit := intArg(args["limit"], 0)
			if offset > 0 || limit > 0 {
				text = selectLines(text, offset, limit)
			}
			truncated := TruncateHead(text, DefaultMaxLines, DefaultMaxBytes)
			return truncated.Content, nil
		},
	}
}

func intArg(v any, fallback int) int {
	n, ok := v.(float64)
	if !ok {
		return fallback
	}
	return int(n)
}

// selectLines implements a 1-indexed offset/limit slice over LF-split lines,
// mirroring the semantics the model-facing read tool documents.
func selectLines(text string, offset, limit int) string {
	lines := strings.Split(text, "\n")
	start := offset - 1
	if start < 0 {
		start = 0
	}
	if start >= len(lines) {
		return ""
	}
	end := len(lines)
	if limit > 0 && start+limit < end {
		end = start + limit
	}
	return strings.Join(lines[start:end], "\n")
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
