package main

import (
	"context"
	"fmt"
	"os"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

func main() {
	path := os.Args[1]
	wasm, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	rt := wazero.NewRuntime(ctx)
	defer rt.Close(ctx)

	wasi := rt.NewHostModuleBuilder("wasi_snapshot_preview1")
	wasi.NewFunctionBuilder().WithFunc(func(ctx context.Context, m api.Module, clockID uint32, precision uint64, resultPtr uint32) uint32 {
		m.Memory().WriteUint64Le(resultPtr, 0)
		return 0
	}).Export("clock_time_get")
	wasi.NewFunctionBuilder().WithFunc(func(ctx context.Context, m api.Module, fd uint32) uint32 { return 0 }).Export("fd_close")
	wasi.NewFunctionBuilder().WithFunc(func(ctx context.Context, m api.Module, fd uint32, statPtr uint32) uint32 { return 0 }).Export("fd_fdstat_get")
	wasi.NewFunctionBuilder().WithFunc(func(ctx context.Context, m api.Module, fd uint32, offset uint64, whence uint32, resultPtr uint32) uint32 {
		return 0
	}).Export("fd_seek")
	wasi.NewFunctionBuilder().WithFunc(func(ctx context.Context, m api.Module, fd uint32, iovs uint32, iovsLen uint32, nwritten uint32) uint32 {
		total := uint32(0)
		for i := uint32(0); i < iovsLen; i++ {
			bufLen, _ := m.Memory().ReadUint32Le(iovs + i*8 + 4)
			total += bufLen
		}
		m.Memory().WriteUint32Le(nwritten, total)
		return 0
	}).Export("fd_write")
	wasi.NewFunctionBuilder().WithFunc(func(ctx context.Context, m api.Module, bufPtr uint32, bufLen uint32) uint32 {
		for i := uint32(0); i < bufLen; i++ {
			m.Memory().WriteByte(bufPtr+i, 0)
		}
		return 0
	}).Export("random_get")

	env := rt.NewHostModuleBuilder("env")
	env.NewFunctionBuilder().WithFunc(func(ctx context.Context, m api.Module, hi, lo uint32) uint32 { return 0 }).Export("host_get_timezone_offset")
	env.NewFunctionBuilder().WithFunc(func(ctx context.Context, m api.Module) uint32 { return 0 }).Export("host_interrupt")
	env.NewFunctionBuilder().WithFunc(func(ctx context.Context, m api.Module, a, b, c uint32) {}).Export("host_promise_rejection")
	env.NewFunctionBuilder().WithFunc(func(ctx context.Context, m api.Module, a, b uint32) uint32 { return 0 }).Export("host_module_normalize")
	env.NewFunctionBuilder().WithFunc(func(ctx context.Context, m api.Module, a, b uint32) uint32 { return 0 }).Export("host_module_load")
	env.NewFunctionBuilder().WithFunc(func(ctx context.Context, m api.Module, namePtr, nameLen, thisPtr, argc, argvPtr uint32) uint32 {
		return 0
	}).Export("host_call")

	if _, err := wasi.Instantiate(ctx); err != nil {
		panic(err)
	}
	if _, err := env.Instantiate(ctx); err != nil {
		panic(err)
	}
	compiled, err := rt.CompileModule(ctx, wasm)
	if err != nil {
		panic(err)
	}
	inst, err := rt.InstantiateModule(ctx, compiled, wazero.NewModuleConfig())
	if err != nil {
		panic(err)
	}

	call := func(name string, args ...uint64) uint64 {
		fn := inst.ExportedFunction(name)
		res, err := fn.Call(ctx, args...)
		if err != nil {
			panic(fmt.Sprintf("%s: %v", name, err))
		}
		if len(res) == 0 {
			return 0
		}
		return res[0]
	}
	call("_initialize")
	call("qjs_init")

	mem := inst.Memory()
	code := []byte("1+1")
	codePtr := uint32(call("wasm_malloc", uint64(len(code)+1)))
	mem.Write(codePtr, code)
	mem.WriteByte(codePtr+uint32(len(code)), 0)
	filename := []byte("test.js")
	filePtr := uint32(call("wasm_malloc", uint64(len(filename)+1)))
	mem.Write(filePtr, filename)
	mem.WriteByte(filePtr+uint32(len(filename)), 0)

	result := uint32(call("qjs_eval", uint64(codePtr), uint64(len(code)), uint64(filePtr), 0))
	if result == 0 {
		fmt.Println("FAIL: eval returned null value pointer")
		os.Exit(1)
	}
	if call("qjs_is_exception", uint64(result)) != 0 {
		fmt.Println("FAIL: eval produced an exception")
		os.Exit(1)
	}
	fmt.Println("PASS: quickjs evaluated a script")
}
