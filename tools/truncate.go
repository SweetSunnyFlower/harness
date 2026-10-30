package tools

import (
	"fmt"
	"strings"
)

const (
	DefaultMaxLines = 2000
	DefaultMaxBytes = 50 * 1024
)

// Truncation describes how tool output text was cut.
type Truncation struct {
	Content               string
	Truncated             bool
	TruncatedBy           string // "lines", "bytes", or ""
	TotalLines            int
	TotalBytes            int
	OutputLines           int
	OutputBytes           int
	LastLinePartial       bool
	FirstLineExceedsLimit bool
	MaxLines              int
	MaxBytes              int
}

func FormatSize(bytes int) string {
	switch {
	case bytes < 1024:
		return fmt.Sprintf("%dB", bytes)
	case bytes < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(bytes)/(1024*1024))
	}
}

// TruncateHead keeps the first maxLines complete lines and maxBytes bytes,
// whichever is hit first. It never returns partial lines except for the
// first-line case, which it reports rather than splitting.
func TruncateHead(content string, maxLines, maxBytes int) Truncation {
	if maxLines <= 0 {
		maxLines = DefaultMaxLines
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	lines := splitLinesForCounting(content)
	totalLines := len(lines)
	totalBytes := len(content)
	if totalLines <= maxLines && totalBytes <= maxBytes {
		return Truncation{
			Content: content, TotalLines: totalLines, TotalBytes: totalBytes,
			OutputLines: totalLines, OutputBytes: totalBytes, MaxLines: maxLines, MaxBytes: maxBytes,
		}
	}
	if len(lines) > 0 && len(lines[0]) > maxBytes {
		return Truncation{
			Truncated: true, TruncatedBy: "bytes", TotalLines: totalLines, TotalBytes: totalBytes,
			FirstLineExceedsLimit: true, MaxLines: maxLines, MaxBytes: maxBytes,
		}
	}
	var out []string
	outputBytes := 0
	truncatedBy := "lines"
	for i := 0; i < len(lines) && i < maxLines; i++ {
		lineBytes := len(lines[i])
		if i > 0 {
			lineBytes++
		}
		if outputBytes+lineBytes > maxBytes {
			truncatedBy = "bytes"
			break
		}
		out = append(out, lines[i])
		outputBytes += lineBytes
	}
	if truncatedBy != "bytes" {
		truncatedBy = "lines"
	}
	text := strings.Join(out, "\n")
	return Truncation{
		Content: text, Truncated: true, TruncatedBy: truncatedBy,
		TotalLines: totalLines, TotalBytes: totalBytes,
		OutputLines: len(out), OutputBytes: len(text),
		MaxLines: maxLines, MaxBytes: maxBytes,
	}
}

func splitLinesForCounting(content string) []string {
	if content == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	if strings.HasSuffix(content, "\n") {
		lines = lines[:len(lines)-1]
	}
	return lines
}
