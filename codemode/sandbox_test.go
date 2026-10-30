package codemode_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"harness/codemode"
)

func newTestSandbox(t *testing.T) *codemode.Sandbox {
	t.Helper()
	wasm, err := os.ReadFile(wasmPath())
	if err != nil {
		t.Skipf("quickjs.wasm not available: %v", err)
	}
	s, err := codemode.NewSandbox(codemode.SandboxOptions{Wasm: wasm})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestSandboxScriptValueAndOutput(t *testing.T) {
	s := newTestSandbox(t)
	got, err := s.Execute(context.Background(), `text("hello"); return "done"`, codemode.ExecuteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !got.OK || got.Value != "done" {
		t.Fatalf("unexpected result: %+v", got)
	}
	if len(got.Output) != 1 || got.Output[0].Text != "hello" {
		t.Fatalf("unexpected output: %+v", got.Output)
	}
}

func TestSandboxToolCall(t *testing.T) {
	s := newTestSandbox(t)
	if err := s.RegisterTool(codemode.Tool{
		Name: "upper",
		Execute: func(ctx context.Context, args any) (any, error) {
			return strings.ToUpper(args.(string)), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Execute(context.Background(), `const r = await tools.upper("hello"); text(r); return r;`, codemode.ExecuteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !got.OK || got.Value != "HELLO" {
		t.Fatalf("unexpected result: %+v", got)
	}
	if len(got.Output) != 1 || got.Output[0].Text != "HELLO" {
		t.Fatalf("unexpected output: %+v", got.Output)
	}
	if len(got.Calls) != 1 || got.Calls[0].Name != "upper" || got.Calls[0].Status != codemode.CallOK {
		t.Fatalf("unexpected calls: %+v", got.Calls)
	}
}

func TestSandboxToolError(t *testing.T) {
	s := newTestSandbox(t)
	if err := s.RegisterTool(codemode.Tool{
		Name: "fail",
		Execute: func(ctx context.Context, args any) (any, error) {
			return nil, os.ErrNotExist
		},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Execute(context.Background(), `try { await tools.fail() } catch (e) { text(e.message) }`, codemode.ExecuteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !got.OK {
		t.Fatalf("expected success: %+v", got)
	}
	if len(got.Output) != 1 || !strings.Contains(got.Output[0].Text, "does not exist") {
		t.Fatalf("unexpected output: %+v", got.Output)
	}
	if len(got.Calls) != 1 || got.Calls[0].Status != codemode.CallError {
		t.Fatalf("unexpected calls: %+v", got.Calls)
	}
}

func TestSandboxScriptError(t *testing.T) {
	s := newTestSandbox(t)
	got, err := s.Execute(context.Background(), `throw new Error("boom")`, codemode.ExecuteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.OK || got.Error == nil || got.Error.Kind != codemode.ErrorScript || got.Error.Message != "boom" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestSandboxSyntaxError(t *testing.T) {
	s := newTestSandbox(t)
	got, err := s.Execute(context.Background(), `const =`, codemode.ExecuteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.OK || got.Error == nil || got.Error.Kind != codemode.ErrorScript || got.Error.Name != "SyntaxError" || got.Error.Message == "" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestSandboxExit(t *testing.T) {
	s := newTestSandbox(t)
	got, err := s.Execute(context.Background(), `text("a"); exit(); text("b")`, codemode.ExecuteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !got.OK || got.Value != nil {
		t.Fatalf("unexpected result: %+v", got)
	}
	if len(got.Output) != 1 || got.Output[0].Text != "a" {
		t.Fatalf("unexpected output: %+v", got.Output)
	}
}

func TestSandboxStore(t *testing.T) {
	s := newTestSandbox(t)
	got, err := s.Execute(context.Background(), `store("k", 42); text(load("k"))`, codemode.ExecuteOptions{
		Store: map[string]any{"other": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got.OK {
		t.Fatalf("unexpected result: %+v", got)
	}
	if got.StoreWrites.Set["k"] != float64(42) {
		t.Fatalf("unexpected writes: %+v", got.StoreWrites)
	}
	if len(got.Output) != 1 || got.Output[0].Text != "42" {
		t.Fatalf("unexpected output: %+v", got.Output)
	}
}

func TestSandboxStalled(t *testing.T) {
	s := newTestSandbox(t)
	got, err := s.Execute(context.Background(), `await new Promise(() => {})`, codemode.ExecuteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.OK || got.Error == nil || got.Error.Kind != codemode.ErrorScript {
		t.Fatalf("unexpected result: %+v", got)
	}
	if !strings.Contains(got.Error.Message, "never settle") {
		t.Fatalf("unexpected error message: %q", got.Error.Message)
	}
}

func TestSandboxGlobal(t *testing.T) {
	s, err := codemode.NewSandbox(codemode.SandboxOptions{
		Wasm: mustWasm(t),
		Globals: []codemode.Tool{{
			Name:   "sum",
			Spread: true,
			Execute: func(ctx context.Context, args any) (any, error) {
				arr := args.([]any)
				return arr[0].(float64) + arr[1].(float64), nil
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Execute(context.Background(), `text(await sum(3, 4))`, codemode.ExecuteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !got.OK || len(got.Output) != 1 || got.Output[0].Text != "7" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestSandboxDeterministicReplay(t *testing.T) {
	s := newTestSandbox(t)
	code := `text(String(Date.now())); text(String(Math.random())); return "ok"`
	opts := codemode.ExecuteOptions{Seed: 42, StartedAt: 1_234_567}
	a, err := s.Execute(context.Background(), code, opts)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Execute(context.Background(), code, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !a.OK || !b.OK || a.Value != b.Value {
		t.Fatalf("replay diverged: a=%+v b=%+v", a, b)
	}
	if len(a.Output) != 2 || len(b.Output) != 2 {
		t.Fatalf("unexpected output: a=%+v b=%+v", a.Output, b.Output)
	}
	for i := range a.Output {
		if a.Output[i].Text != b.Output[i].Text {
			t.Fatalf("replay output diverged: %q vs %q", a.Output[i].Text, b.Output[i].Text)
		}
	}
}

func mustWasm(t *testing.T) []byte {
	t.Helper()
	wasm, err := os.ReadFile(wasmPath())
	if err != nil {
		t.Skipf("quickjs.wasm not available: %v", err)
	}
	return wasm
}
