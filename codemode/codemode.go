package codemode

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

type virtualClock struct {
	creating  bool
	startedAt int64
	now       int64
	seed      uint32
	state     uint32
	seeded    bool
}

func (c *virtualClock) nowNs(clockID uint32) uint64 {
	if c.creating {
		if clockID == 0 {
			return uint64(c.seed+1) * 1000
		}
		return 0
	}
	if clockID == 0 {
		return uint64(c.now) * 1_000_000
	}
	return uint64(c.now-c.startedAt) * 1_000_000
}

func (c *virtualClock) fillRandom(m api.Module, bufPtr, bufLen uint32) {
	if !c.seeded {
		c.state = c.seed ^ 0x9e3779b9
		c.seeded = true
	}
	var buf [4]byte
	for i := uint32(0); i < bufLen; i += 4 {
		c.state += 0x6d2b79f5
		v := c.state
		v ^= v >> 15
		v *= 1 | v
		v ^= v + v*61
		v ^= v >> 14
		binary.LittleEndian.PutUint32(buf[:], v)
		for j := uint32(0); j < 4 && i+j < bufLen; j++ {
			m.Memory().WriteByte(bufPtr+i+j, buf[j])
		}
	}
}

type clockHolder struct {
	clock *virtualClock
}

// VM runs quickjs-wasi with a deterministic clock and seeded randomness.
type VM struct {
	runtime  wazero.Runtime
	compiled wazero.CompiledModule
	holder   *clockHolder
}

func Open(ctx context.Context, wasm []byte) (*VM, error) {
	rt := wazero.NewRuntime(ctx)
	holder := &clockHolder{}

	wasi := rt.NewHostModuleBuilder("wasi_snapshot_preview1")
	wasi.NewFunctionBuilder().WithFunc(func(ctx context.Context, m api.Module, clockID uint32, precision uint64, resultPtr uint32) uint32 {
		m.Memory().WriteUint64Le(resultPtr, holder.clock.nowNs(clockID))
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
			l, _ := m.Memory().ReadUint32Le(iovs + i*8 + 4)
			total += l
		}
		m.Memory().WriteUint32Le(nwritten, total)
		return 0
	}).Export("fd_write")
	wasi.NewFunctionBuilder().WithFunc(func(ctx context.Context, m api.Module, bufPtr uint32, bufLen uint32) uint32 {
		holder.clock.fillRandom(m, bufPtr, bufLen)
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
		rt.Close(ctx)
		return nil, err
	}
	if _, err := env.Instantiate(ctx); err != nil {
		rt.Close(ctx)
		return nil, err
	}
	compiled, err := rt.CompileModule(ctx, wasm)
	if err != nil {
		rt.Close(ctx)
		return nil, err
	}
	return &VM{runtime: rt, compiled: compiled, holder: holder}, nil
}

func (v *VM) Eval(ctx context.Context, code string, startedAt int64, seed uint32) (string, error) {
	inst, err := v.runtime.InstantiateModule(ctx, v.compiled, wazero.NewModuleConfig())
	if err != nil {
		return "", err
	}
	defer inst.Close(ctx)
	clock := &virtualClock{creating: true, startedAt: startedAt, now: startedAt, seed: seed}
	v.holder.clock = clock
	if _, err := inst.ExportedFunction("_initialize").Call(ctx); err != nil {
		return "", err
	}
	if _, err := inst.ExportedFunction("qjs_init").Call(ctx); err != nil {
		return "", err
	}
	clock.creating = false
	return v.evalString(ctx, inst, code)
}

func (v *VM) evalString(ctx context.Context, inst api.Module, code string) (string, error) {
	call := func(name string, args ...uint64) (uint64, error) {
		res, err := inst.ExportedFunction(name).Call(ctx, args...)
		if err != nil {
			return 0, err
		}
		if len(res) == 0 {
			return 0, nil
		}
		return res[0], nil
	}
	mem := inst.Memory()
	codeBytes := []byte(code)
	codePtr, err := call("wasm_malloc", uint64(len(codeBytes)+1))
	if err != nil {
		return "", err
	}
	mem.Write(uint32(codePtr), codeBytes)
	mem.WriteByte(uint32(codePtr)+uint32(len(codeBytes)), 0)
	name := []byte("test.js")
	namePtr, _ := call("wasm_malloc", uint64(len(name)+1))
	mem.Write(uint32(namePtr), name)
	mem.WriteByte(uint32(namePtr)+uint32(len(name)), 0)

	result, err := call("qjs_eval", codePtr, uint64(len(codeBytes)), namePtr, 0)
	if err != nil {
		return "", err
	}
	if result == 0 {
		return "", fmt.Errorf("eval returned null")
	}
	if exc, _ := call("qjs_is_exception", result); exc != 0 {
		return "", fmt.Errorf("eval exception")
	}
	lenPtr, _ := call("wasm_malloc", 4)
	cstr, _ := call("qjs_get_string_len", result, lenPtr)
	n, _ := mem.ReadUint32Le(uint32(lenPtr))
	data, _ := mem.Read(uint32(cstr), n)
	_, _ = call("qjs_free_cstring", cstr)
	_, _ = call("wasm_free", lenPtr)
	_, _ = call("qjs_free_value", result)
	return string(data), nil
}

func (v *VM) Close(ctx context.Context) error {
	return v.runtime.Close(ctx)
}
