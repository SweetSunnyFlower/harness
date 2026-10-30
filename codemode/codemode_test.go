package codemode_test

import (
	"context"
	"os"
	"testing"

	"harness/codemode"
)

func wasmPath() string {
	p := os.Getenv("QUICKJS_WASM")
	if p == "" {
		p = "/tmp/pi-ts-interop/node_modules/quickjs-wasi/quickjs.wasm"
	}
	return p
}

func TestDeterministicRandom(t *testing.T) {
	wasm, err := os.ReadFile(wasmPath())
	if err != nil {
		t.Skipf("quickjs.wasm not available: %v", err)
	}
	ctx := context.Background()
	vm, err := codemode.Open(ctx, wasm)
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close(ctx)
	a, err := vm.Eval(ctx, "String(Math.random())", 1000, 1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := vm.Eval(ctx, "String(Math.random())", 1000, 1)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("same seed differs: %q vs %q", a, b)
	}
	c, err := vm.Eval(ctx, "String(Math.random())", 1000, 2)
	if err != nil {
		t.Fatal(err)
	}
	if c == a {
		t.Fatalf("different seeds agree: %q", a)
	}
}

func TestEvalWithHost(t *testing.T) {
	wasm, err := os.ReadFile(wasmPath())
	if err != nil {
		t.Skipf("quickjs.wasm not available: %v", err)
	}
	ctx := context.Background()
	vm, err := codemode.Open(ctx, wasm)
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close(ctx)

	got, err := vm.EvalWithHost(ctx, `String(echo("hello")) + "!"`, "echo", 7, 42, func(args []string) string {
		if len(args) != 1 {
			return "wrong-argc"
		}
		return "ECHO:" + args[0]
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "ECHO:hello!" {
		t.Fatalf("unexpected result: %q", got)
	}
}
