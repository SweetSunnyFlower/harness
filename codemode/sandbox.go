package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

const defaultTimeoutMs int64 = 300_000

var identifierPattern = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

var reservedGlobals = map[string]bool{
	"tools": true, "ALL_TOOLS": true, "console": true, "text": true,
	"image": true, "exit": true, "globalThis": true, "store": true, "load": true,
}

// Sandbox runs JavaScript in a QuickJS VM with the pi codemode prelude: the
// script sees tools.<name>(args), ALL_TOOLS, text/image/exit/console, store/load,
// and configured globals, and nothing else.
type Sandbox struct {
	mu      sync.Mutex
	tools   map[string]Tool
	globals map[string]Tool
	timeout int64
	memory  int64
	wasm    []byte
	closed  bool
}

func NewSandbox(opts SandboxOptions) (*Sandbox, error) {
	s := &Sandbox{
		tools:   map[string]Tool{},
		globals: map[string]Tool{},
		timeout: opts.TimeoutMs,
		memory:  opts.MemoryLimitBytes,
		wasm:    opts.Wasm,
	}
	if s.timeout == 0 {
		s.timeout = defaultTimeoutMs
	}
	for _, tool := range opts.Tools {
		if err := s.RegisterTool(tool); err != nil {
			return nil, err
		}
	}
	namespaces := map[string]bool{}
	for _, g := range opts.Globals {
		if err := validateGlobalName(g.Name); err != nil {
			return nil, err
		}
		if _, ok := s.globals[g.Name]; ok {
			return nil, fmt.Errorf("global %q is already registered", g.Name)
		}
		if dot := strings.IndexByte(g.Name, '.'); dot != -1 {
			namespaces[g.Name[:dot]] = true
		}
		s.globals[g.Name] = g
	}
	for ns := range namespaces {
		if _, ok := s.globals[ns]; ok {
			return nil, fmt.Errorf("global %q conflicts with the namespace %q", ns, ns)
		}
	}
	return s, nil
}

func validateGlobalName(name string) error {
	if name == "" {
		return errors.New("global name must not be empty")
	}
	parts := strings.Split(name, ".")
	if len(parts) > 2 {
		return fmt.Errorf("invalid global name %q", name)
	}
	for _, p := range parts {
		if !identifierPattern.MatchString(p) {
			return fmt.Errorf("invalid global name %q", name)
		}
	}
	if reservedGlobals[parts[0]] {
		return fmt.Errorf("invalid global name %q", name)
	}
	return nil
}

func (s *Sandbox) RegisterTool(tool Tool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("sandbox is closed")
	}
	if _, ok := s.tools[tool.Name]; ok {
		return fmt.Errorf("tool %q is already registered", tool.Name)
	}
	s.tools[tool.Name] = tool
	return nil
}

func (s *Sandbox) UnregisterTool(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tools[name]; !ok {
		return false
	}
	delete(s.tools, name)
	return true
}

func (s *Sandbox) Tools() []Tool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Tool, 0, len(s.tools))
	for _, t := range s.tools {
		out = append(out, t)
	}
	return out
}

func (s *Sandbox) Globals() []Tool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Tool, 0, len(s.globals))
	for _, g := range s.globals {
		out = append(out, g)
	}
	return out
}

// Execute runs code. Script failures are returned in Result.Error, never as
// the second return value; that is reserved for sandbox-level failures such as
// a closed sandbox or a missing wasm binary.
func (s *Sandbox) Execute(parent context.Context, code string, opts ExecuteOptions) (Result, error) {
	if parent == nil {
		parent = context.Background()
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return Result{}, errors.New("sandbox is closed")
	}
	timeout := s.timeout
	wasm := s.wasm
	s.mu.Unlock()

	parsed, err := ParseCodemodeSource(code)
	if err != nil {
		return Result{}, err
	}
	if opts.TimeoutMs != 0 {
		timeout = opts.TimeoutMs
	}

	execCtx := parent
	cancel := func() {}
	if timeout > 0 {
		execCtx, cancel = context.WithTimeout(parent, time.Duration(timeout)*time.Millisecond)
	} else {
		execCtx, cancel = context.WithCancel(parent)
	}
	defer cancel()

	vm, err := Open(execCtx, wasm)
	if err != nil {
		return Result{}, &Error{Kind: ErrorSandbox, Message: "failed to load QuickJS: " + err.Error()}
	}
	defer vm.Close(context.Background())

	inst, err := vm.newInstance(execCtx, opts.StartedAt, opts.Seed)
	if err != nil {
		return Result{}, &Error{Kind: ErrorSandbox, Message: "failed to start QuickJS: " + err.Error()}
	}
	defer inst.close()

	ex := &execution{
		sandbox: s,
		inst:    inst,
		ctx:     execCtx,
		state:   &execState{signaled: make(chan struct{}, 1)},
		result:  Result{OK: false, Output: []OutputItem{}, Calls: []Call{}, StoreWrites: StoreWrites{Set: map[string]any{}, Delete: []string{}}},
	}

	vm.RegisterHostFunc("bridge", ex.bridge)

	bridgeFn, err := inst.newHostFunction("bridge")
	if err != nil {
		return Result{}, &Error{Kind: ErrorSandbox, Message: err.Error()}
	}
	defer inst.freeValue(bridgeFn)

	preludeFn, err := inst.eval(preludeSource, "codemode-prelude.js")
	if err != nil {
		return Result{}, &Error{Kind: ErrorSandbox, Message: "prelude evaluation failed: " + err.Error()}
	}
	defer inst.freeValue(preludeFn)
	if inst.isException(preludeFn) {
		e, _ := inst.currentException()
		return Result{}, &Error{Kind: ErrorSandbox, Message: "prelude evaluation failed: " + e.Message}
	}

	toolsJSON, _ := json.Marshal(ex.toolList())
	globalsJSON, _ := json.Marshal(ex.globalList())
	storeJSON, _ := json.Marshal(serializeStore(opts.Store))
	toolsHandle, err := inst.newString(string(toolsJSON))
	if err != nil {
		return Result{}, &Error{Kind: ErrorSandbox, Message: err.Error()}
	}
	defer inst.freeValue(toolsHandle)
	globalsHandle, err := inst.newString(string(globalsJSON))
	if err != nil {
		return Result{}, &Error{Kind: ErrorSandbox, Message: err.Error()}
	}
	defer inst.freeValue(globalsHandle)
	storeHandle, err := inst.newString(string(storeJSON))
	if err != nil {
		return Result{}, &Error{Kind: ErrorSandbox, Message: err.Error()}
	}
	defer inst.freeValue(storeHandle)

	api, err := inst.callFunction(preludeFn, inst.undefined(), bridgeFn, toolsHandle, globalsHandle, storeHandle)
	if err != nil {
		return Result{}, &Error{Kind: ErrorSandbox, Message: "prelude call failed: " + err.Error()}
	}
	defer inst.freeValue(api)
	if inst.isException(api) {
		e, _ := inst.currentException()
		return Result{}, &Error{Kind: ErrorSandbox, Message: "prelude call failed: " + e.Message}
	}

	ex.api = api
	ex.settleFn, err = inst.getPropString(api, "settle")
	if err != nil {
		return Result{}, &Error{Kind: ErrorSandbox, Message: err.Error()}
	}
	ex.runFn, err = inst.getPropString(api, "run")
	if err != nil {
		return Result{}, &Error{Kind: ErrorSandbox, Message: err.Error()}
	}
	ex.stalledFn, err = inst.getPropString(api, "stalled")
	if err != nil {
		return Result{}, &Error{Kind: ErrorSandbox, Message: err.Error()}
	}
	defer func() {
		inst.freeValue(ex.settleFn)
		inst.freeValue(ex.runFn)
		inst.freeValue(ex.stalledFn)
	}()

	fn, err := inst.eval("(async (tools, console) => {"+parsed.Code+"\n})", "codemode.js")
	if err != nil {
		return Result{}, &Error{Kind: ErrorSandbox, Message: "script evaluation failed: " + err.Error()}
	}
	defer inst.freeValue(fn)
	if inst.isException(fn) {
		return ex.scriptErrorResult()
	}

	runRes, err := inst.callFunction(ex.runFn, api, fn)
	if err != nil {
		return Result{}, &Error{Kind: ErrorSandbox, Message: "script run failed: " + err.Error()}
	}
	if inst.isException(runRes) {
		inst.freeValue(runRes)
		return ex.scriptErrorResult()
	}
	inst.freeValue(runRes)

	if err := ex.drain(); err != nil {
		return Result{}, &Error{Kind: ErrorSandbox, Message: err.Error()}
	}

	for !ex.finished {
		ex.processMessages()
		if ex.finished {
			break
		}
		if execCtx.Err() != nil {
			ex.finishContext(execCtx.Err())
			break
		}
		if err := ex.checkStalled(); err != nil {
			return Result{}, &Error{Kind: ErrorSandbox, Message: err.Error()}
		}
		ex.processMessages()
		if ex.finished {
			break
		}
		select {
		case <-ex.state.signaled:
		case <-execCtx.Done():
			ex.finishContext(execCtx.Err())
		}
	}
	return ex.result, nil
}

// Close aborts in-flight executions and rejects new ones. In-flight Execute
// calls observe cancellation through their context.
func (s *Sandbox) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

type toolDescriptor struct {
	Name        string `json:"name"`
	JSName      string `json:"jsName"`
	Description string `json:"description"`
}

type globalDescriptor struct {
	Name   string `json:"name"`
	Spread bool   `json:"spread"`
}

func (ex *execution) toolList() []toolDescriptor {
	out := make([]toolDescriptor, 0, len(ex.sandbox.tools))
	for _, t := range ex.sandbox.tools {
		out = append(out, toolDescriptor{Name: t.Name, JSName: ToCodemodeIdentifier(t.Name), Description: t.Description})
	}
	return out
}

func (ex *execution) globalList() []globalDescriptor {
	out := make([]globalDescriptor, 0, len(ex.sandbox.globals))
	for _, g := range ex.sandbox.globals {
		out = append(out, globalDescriptor{Name: g.Name, Spread: g.Spread})
	}
	return out
}

func serializeStore(store map[string]any) map[string]string {
	out := map[string]string{}
	for key, value := range store {
		b, err := json.Marshal(value)
		if err == nil {
			out[key] = string(b)
		}
	}
	return out
}

type bridgeMessage struct {
	kind       string
	id         uint32
	name       string
	args       *string
	outputKind string
	text       string
	mimeType   string
	ok         bool
	value      *string
	writes     *string
	errMsg     string
}

type execState struct {
	mu       sync.Mutex
	msgs     []bridgeMessage
	signaled chan struct{}
}

func (e *execState) push(m bridgeMessage) {
	e.mu.Lock()
	e.msgs = append(e.msgs, m)
	e.mu.Unlock()
	select {
	case e.signaled <- struct{}{}:
	default:
	}
}

func (e *execState) pop() (bridgeMessage, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.msgs) == 0 {
		return bridgeMessage{}, false
	}
	m := e.msgs[0]
	e.msgs = e.msgs[1:]
	return m, true
}

type execution struct {
	sandbox                    *Sandbox
	inst                       *instance
	ctx                        context.Context
	state                      *execState
	result                     Result
	api                        uint32
	settleFn, runFn, stalledFn uint32
	finished                   bool
}

func (ex *execution) bridge(call HostCall) (uint32, error) {
	if len(call.Args) < 3 {
		return ex.inst.undefined(), nil
	}
	kind, _ := ex.inst.readString(call.Args[0])
	switch kind {
	case "call", "global":
		id := uint32(ex.inst.getNumber(call.Args[1]))
		name, _ := ex.inst.readString(call.Args[2])
		var args *string
		if len(call.Args) > 3 && !ex.inst.isUndefined(call.Args[3]) {
			if s, err := ex.inst.readString(call.Args[3]); err == nil {
				args = &s
			}
		}
		ex.state.push(bridgeMessage{kind: kind, id: id, name: name, args: args})
	case "output":
		outputKind, _ := ex.inst.readString(call.Args[1])
		text, _ := ex.inst.readString(call.Args[2])
		mime, _ := ex.inst.readString(call.Args[3])
		ex.state.push(bridgeMessage{kind: "output", outputKind: outputKind, text: text, mimeType: mime})
	case "done":
		ok := ex.inst.getBool(call.Args[1])
		if ok {
			var value *string
			if len(call.Args) > 2 && !ex.inst.isUndefined(call.Args[2]) {
				if s, err := ex.inst.readString(call.Args[2]); err == nil {
					value = &s
				}
			}
			var writes *string
			if len(call.Args) > 3 {
				if s, err := ex.inst.readString(call.Args[3]); err == nil {
					writes = &s
				}
			}
			ex.state.push(bridgeMessage{kind: "done", ok: true, value: value, writes: writes})
		} else {
			errMsg, _ := ex.inst.readString(call.Args[2])
			ex.state.push(bridgeMessage{kind: "done", ok: false, errMsg: errMsg})
		}
	}
	return ex.inst.undefined(), nil
}

func (ex *execution) drain() error {
	for ex.inst.isJobPending() {
		code, err := ex.inst.executePendingJob()
		if err != nil {
			return err
		}
		if code < 0 {
			e, err := ex.inst.currentException()
			if err != nil {
				return err
			}
			return errors.New(e.Message)
		}
	}
	return nil
}

func (ex *execution) processMessages() {
	for {
		m, ok := ex.state.pop()
		if !ok {
			return
		}
		switch m.kind {
		case "call", "global":
			if err := ex.handleCall(m); err != nil {
				ex.finish(&Error{Kind: ErrorSandbox, Message: err.Error()})
				return
			}
		case "output":
			ex.handleOutput(m)
		case "done":
			ex.handleDone(m)
			return
		}
		if ex.finished {
			return
		}
	}
}

func (ex *execution) handleOutput(m bridgeMessage) {
	if m.outputKind == "image" {
		ex.result.Output = append(ex.result.Output, OutputItem{Type: "image", Data: m.text, MimeType: m.mimeType})
		return
	}
	ex.result.Output = append(ex.result.Output, OutputItem{Type: "text", Text: m.text, Console: m.outputKind == "console"})
}

func (ex *execution) handleCall(m bridgeMessage) error {
	isTool := m.kind == "call"
	target := "global"
	if isTool {
		target = "tool"
	}
	record := Call{Name: m.name, Status: CallCancelled}
	if isTool {
		ex.result.Calls = append(ex.result.Calls, record)
	}
	start := time.Now()
	tool, ok := func() (Tool, bool) {
		if isTool {
			t, ok := ex.sandbox.tools[m.name]
			return t, ok
		}
		t, ok := ex.sandbox.globals[m.name]
		return t, ok
	}()

	status := CallOK
	var payload *string
	if !ok {
		s := fmt.Sprintf("Unknown %s %q", target, m.name)
		payload = &s
		status = CallError
	} else {
		var args any
		if m.args != nil {
			if err := json.Unmarshal([]byte(*m.args), &args); err != nil {
				s := err.Error()
				payload = &s
				status = CallError
			}
		}
		if status == CallOK {
			value, err := tool.Execute(ex.ctx, args)
			if err != nil {
				s := err.Error()
				payload = &s
				status = CallError
			} else if value != nil {
				b, merr := json.Marshal(value)
				if merr != nil {
					s := merr.Error()
					payload = &s
					status = CallError
				} else {
					s := string(b)
					payload = &s
				}
			}
		}
	}
	record.Status = status
	record.DurationMs = float64(time.Since(start)) / float64(time.Millisecond)
	if isTool {
		ex.result.Calls[len(ex.result.Calls)-1] = record
	}
	if err := ex.settle(m.id, status == CallOK, payload); err != nil {
		return err
	}
	return ex.drain()
}

func (ex *execution) settle(id uint32, ok bool, payload *string) error {
	idHandle, err := ex.inst.newNumber(float64(id))
	if err != nil {
		return err
	}
	defer ex.inst.freeValue(idHandle)
	okHandle := ex.inst.falseValue()
	if ok {
		okHandle = ex.inst.trueValue()
	}
	defer ex.inst.freeValue(okHandle)
	payloadHandle := ex.inst.undefined()
	if payload != nil {
		h, err := ex.inst.newString(*payload)
		if err != nil {
			return err
		}
		payloadHandle = h
	}
	defer ex.inst.freeValue(payloadHandle)
	res, err := ex.inst.callFunction(ex.settleFn, ex.api, idHandle, okHandle, payloadHandle)
	if err != nil {
		return err
	}
	defer ex.inst.freeValue(res)
	if ex.inst.isException(res) {
		e, err := ex.inst.currentException()
		if err != nil {
			return err
		}
		return errors.New(e.Message)
	}
	return nil
}

func (ex *execution) handleDone(m bridgeMessage) {
	if !m.ok {
		e, err := parseScriptError(m.errMsg)
		if err != nil {
			e = &Error{Kind: ErrorSandbox, Message: "sandbox bridge broken: " + err.Error()}
		}
		ex.finish(e)
		return
	}
	var value any
	if m.value != nil {
		v, err := parseBridgeJSON(*m.value, "return value")
		if err != nil {
			ex.finish(&Error{Kind: ErrorSandbox, Message: "sandbox bridge broken: " + err.Error()})
			return
		}
		value = v
	}
	writes := StoreWrites{Set: map[string]any{}, Delete: []string{}}
	if m.writes != nil {
		w, err := parseStoreWrites(*m.writes)
		if err != nil {
			ex.finish(&Error{Kind: ErrorSandbox, Message: "sandbox bridge broken: " + err.Error()})
			return
		}
		writes = w
	}
	ex.finishValue(value, writes)
}

func (ex *execution) checkStalled() error {
	res, err := ex.inst.callFunction(ex.stalledFn, ex.api)
	if err != nil {
		return err
	}
	defer ex.inst.freeValue(res)
	if ex.inst.isException(res) {
		e, err := ex.inst.currentException()
		if err != nil {
			return err
		}
		return errors.New(e.Message)
	}
	return nil
}

func (ex *execution) scriptErrorResult() (Result, error) {
	e, err := ex.inst.currentException()
	if err != nil {
		return Result{}, &Error{Kind: ErrorSandbox, Message: err.Error()}
	}
	ex.result.Error = e
	return ex.result, nil
}

func (ex *execution) finish(e *Error) {
	if ex.finished {
		return
	}
	ex.finished = true
	ex.result.Error = e
	ex.result.OK = false
}

func (ex *execution) finishValue(value any, writes StoreWrites) {
	if ex.finished {
		return
	}
	ex.finished = true
	ex.result.OK = true
	ex.result.Value = value
	ex.result.StoreWrites = writes
}

func (ex *execution) finishContext(err error) {
	kind := ErrorAborted
	message := "execution aborted"
	if errors.Is(err, context.DeadlineExceeded) {
		kind = ErrorTimeout
		message = "execution timed out"
	} else if err != nil {
		message = err.Error()
	}
	ex.finish(&Error{Kind: kind, Message: message})
}

func parseBridgeJSON(jsonText, what string) (any, error) {
	var v any
	if err := json.Unmarshal([]byte(jsonText), &v); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON", what)
	}
	return v, nil
}

func parseScriptError(jsonText string) (*Error, error) {
	v, err := parseBridgeJSON(jsonText, "script error")
	if err != nil {
		return nil, err
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("script error is not an object")
	}
	message, ok := obj["message"].(string)
	if !ok {
		return nil, errors.New("script error is malformed")
	}
	name, _ := obj["name"].(string)
	stack, _ := obj["stack"].(string)
	if name == "" {
		name = "Error"
	}
	return &Error{Kind: ErrorScript, Name: name, Message: message, Stack: stack}, nil
}

func parseStoreWrites(jsonText string) (StoreWrites, error) {
	v, err := parseBridgeJSON(jsonText, "store writes")
	if err != nil {
		return StoreWrites{}, err
	}
	entries, ok := v.([]any)
	if !ok {
		return StoreWrites{}, errors.New("store writes are not an array")
	}
	writes := StoreWrites{Set: map[string]any{}, Delete: []string{}}
	for _, entryAny := range entries {
		entry, ok := entryAny.([]any)
		if !ok || len(entry) == 0 || len(entry) > 2 {
			return StoreWrites{}, errors.New("store writes contain a malformed entry")
		}
		key, ok := entry[0].(string)
		if !ok {
			return StoreWrites{}, errors.New("store writes contain a malformed entry")
		}
		if len(entry) == 1 {
			writes.Delete = append(writes.Delete, key)
			continue
		}
		valueJSON, ok := entry[1].(string)
		if !ok {
			return StoreWrites{}, errors.New("store writes contain a malformed entry")
		}
		value, err := parseBridgeJSON(valueJSON, fmt.Sprintf("store value for %q", key))
		if err != nil {
			return StoreWrites{}, err
		}
		writes.Set[key] = value
	}
	return writes, nil
}
