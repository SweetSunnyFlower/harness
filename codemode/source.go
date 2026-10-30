package codemode

import (
	"encoding/json"
	"fmt"
	"strings"
)

// CODEMODE_OPTIONS_PREFIX marks an optional leading options line.
const CODEMODE_OPTIONS_PREFIX = "// @options:"

// CodemodeSourceOptions carries the optional per-script @options values.
type CodemodeSourceOptions struct {
	MaxOutputTokens int
	TimeoutMs       int
}

// ParsedCodemodeSource is a script with its options line replaced by an empty
// line, so reported line numbers still match the input as written.
type ParsedCodemodeSource struct {
	Code    string
	Options CodemodeSourceOptions
}

// CodemodeSourceError describes an invalid codemode source.
type CodemodeSourceError struct {
	Message string
}

func (e *CodemodeSourceError) Error() string { return e.Message }

const maxTimeoutMs = 2_147_483_647

func parseCodemodeSource(input string) (ParsedCodemodeSource, error) {
	if strings.TrimSpace(input) == "" {
		return ParsedCodemodeSource{}, &CodemodeSourceError{Message: "Expected JavaScript source text (non-empty). Provide JS only, optionally with a first line `// @options: {\"max_output_tokens\": 1000}`."}
	}
	newline := strings.IndexByte(input, '\n')
	firstLine := input
	if newline != -1 {
		firstLine = input[:newline]
	}
	firstLine = strings.TrimSuffix(firstLine, "\r")
	trimmed := strings.TrimLeft(firstLine, " \t")
	if !strings.HasPrefix(trimmed, CODEMODE_OPTIONS_PREFIX) {
		return ParsedCodemodeSource{Code: input}, nil
	}
	code := ""
	if newline != -1 {
		code = input[newline:]
	}
	if strings.TrimSpace(code) == "" {
		return ParsedCodemodeSource{}, &CodemodeSourceError{Message: "The @options line must be followed by JavaScript source on subsequent lines"}
	}
	opts, err := parseCodemodeOptions(strings.TrimSpace(strings.TrimPrefix(trimmed, CODEMODE_OPTIONS_PREFIX)))
	if err != nil {
		return ParsedCodemodeSource{}, err
	}
	return ParsedCodemodeSource{Code: code, Options: opts}, nil
}

func parseCodemodeOptions(directive string) (CodemodeSourceOptions, error) {
	const supported = "`max_output_tokens` and `timeout_ms`"
	if directive == "" {
		return CodemodeSourceOptions{}, &CodemodeSourceError{Message: "@options must be a JSON object with supported fields " + supported}
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(directive), &fields); err != nil {
		return CodemodeSourceOptions{}, &CodemodeSourceError{Message: fmt.Sprintf("@options must be valid JSON with supported fields %s: %v", supported, err)}
	}
	if fields == nil {
		return CodemodeSourceOptions{}, &CodemodeSourceError{Message: "@options must be a JSON object with supported fields " + supported}
	}
	for key := range fields {
		if key != "max_output_tokens" && key != "timeout_ms" {
			return CodemodeSourceOptions{}, &CodemodeSourceError{Message: fmt.Sprintf("@options only supports %s; got `%s`", supported, key)}
		}
	}
	var opts CodemodeSourceOptions
	if v, ok := fields["max_output_tokens"]; ok {
		n, ok := nonNegativeInt(v)
		if !ok {
			return CodemodeSourceOptions{}, &CodemodeSourceError{Message: "@options field `max_output_tokens` must be a non-negative safe integer"}
		}
		opts.MaxOutputTokens = n
	}
	if v, ok := fields["timeout_ms"]; ok {
		n, ok := nonNegativeInt(v)
		if !ok || n == 0 || n > maxTimeoutMs {
			return CodemodeSourceOptions{}, &CodemodeSourceError{Message: fmt.Sprintf("@options field `timeout_ms` must be a positive integer up to %d", maxTimeoutMs)}
		}
		opts.TimeoutMs = n
	}
	return opts, nil
}

func nonNegativeInt(v any) (int, bool) {
	n, ok := v.(float64)
	if !ok || n < 0 || n != float64(int(n)) {
		return 0, false
	}
	return int(n), true
}
