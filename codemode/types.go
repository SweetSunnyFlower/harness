package codemode

import "context"

// Tool is a function the script calls as tools.<name>(args) (or, for globals,
// directly as <name>(args)). `Name` is the script-visible identifier; for a
// Tool it is normalised with toCodemodeIdentifier, while a Global must already
// be a valid identifier or namespace.member.
type Tool struct {
	Name        string
	Description string
	Spread      bool
	Signature   string
	Execute     func(ctx context.Context, args any) (any, error)
}

// OutputItem is one item of the script's output, in the order produced.
type OutputItem struct {
	Type     string `json:"type"` // "text" or "image"
	Text     string `json:"text,omitempty"`
	Console  bool   `json:"console,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// CallStatus is the final status of one tool call.
type CallStatus string

const (
	CallOK        CallStatus = "ok"
	CallError     CallStatus = "error"
	CallCancelled CallStatus = "cancelled"
)

// Call records one tool invocation for the result.
type Call struct {
	Name       string
	Status     CallStatus
	DurationMs float64
}

// ErrorKind classifies a CodemodeError.
type ErrorKind string

const (
	ErrorScript  ErrorKind = "script"
	ErrorTimeout ErrorKind = "timeout"
	ErrorAborted ErrorKind = "aborted"
	ErrorSandbox ErrorKind = "sandbox"
)

// Error is a script or sandbox failure.
type Error struct {
	Kind    ErrorKind
	Name    string
	Message string
	Stack   string
}

func (e *Error) Error() string { return e.Message }

// StoreWrites reports the keys the script changed with store().
type StoreWrites struct {
	Set    map[string]any
	Delete []string
}

// Result is the outcome of one Sandbox.Execute call. Script failures come back
// as {OK:false, Error:...}, never as the returned error.
type Result struct {
	OK          bool
	Value       any
	Error       *Error
	Output      []OutputItem
	Calls       []Call
	StoreWrites StoreWrites
}

// SandboxOptions configures a Sandbox.
type SandboxOptions struct {
	Tools            []Tool
	Globals          []Tool
	TimeoutMs        int64
	MemoryLimitBytes int64
	Wasm             []byte
}

// ExecuteOptions overrides per-execution settings.
type ExecuteOptions struct {
	TimeoutMs int64
	Store     map[string]any
}
