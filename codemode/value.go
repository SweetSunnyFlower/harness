package codemode

import (
	"context"
	"fmt"
	"math"

	"github.com/tetratelabs/wazero/api"
)

// instance wraps one instantiated quickjs-wasi module.
type instance struct {
	m   api.Module
	mem api.Memory
	ctx context.Context
}

func (i *instance) close() {
	if i.m != nil {
		_ = i.m.Close(context.Background())
	}
}

func (i *instance) call(name string, args ...uint64) (uint64, error) {
	return i.callCtx(i.ctx, name, args...)
}

func (i *instance) callCtx(ctx context.Context, name string, args ...uint64) (uint64, error) {
	res, err := i.m.ExportedFunction(name).Call(ctx, args...)
	if err != nil {
		return 0, err
	}
	if len(res) == 0 {
		return 0, nil
	}
	return res[0], nil
}

func (i *instance) malloc(n uint64) (uint32, error) {
	p, err := i.call("wasm_malloc", n)
	return uint32(p), err
}

func (i *instance) free(p uint32) {
	if p != 0 {
		_, _ = i.callCtx(context.Background(), "wasm_free", uint64(p))
	}
}

func (i *instance) writeCString(s string) (uint32, error) {
	b := []byte(s)
	p, err := i.malloc(uint64(len(b) + 1))
	if err != nil {
		return 0, err
	}
	i.mem.Write(p, b)
	i.mem.WriteByte(p+uint32(len(b)), 0)
	return p, nil
}

func (i *instance) readCString(p, n uint32) string {
	if p == 0 {
		return ""
	}
	data, _ := i.mem.Read(p, n)
	return string(data)
}

func (i *instance) newString(s string) (uint32, error) {
	b := []byte(s)
	p, err := i.malloc(uint64(len(b) + 1))
	if err != nil {
		return 0, err
	}
	i.mem.Write(p, b)
	i.mem.WriteByte(p+uint32(len(b)), 0)
	v, err := i.call("qjs_new_string", uint64(p), uint64(len(b)))
	i.free(p)
	return uint32(v), err
}

func (i *instance) readString(h uint32) (string, error) {
	lenPtr, err := i.malloc(4)
	if err != nil {
		return "", err
	}
	cstr, err := i.call("qjs_get_string_len", uint64(h), uint64(lenPtr))
	if err != nil {
		i.free(lenPtr)
		return "", err
	}
	n, _ := i.mem.ReadUint32Le(lenPtr)
	s := i.readCString(uint32(cstr), n)
	if cstr != 0 {
		_, _ = i.callCtx(context.Background(), "qjs_free_cstring", cstr)
	}
	i.free(lenPtr)
	return s, nil
}

func (i *instance) isUndefined(h uint32) bool {
	r, err := i.call("qjs_is_undefined", uint64(h))
	return err == nil && r != 0
}

func (i *instance) isException(h uint32) bool {
	r, err := i.call("qjs_is_exception", uint64(h))
	return err == nil && r != 0
}

func (i *instance) getBool(h uint32) bool {
	r, err := i.call("qjs_get_bool", uint64(h))
	return err == nil && r != 0
}

func (i *instance) getNumber(h uint32) float64 {
	r, err := i.call("qjs_get_float64", uint64(h))
	if err != nil {
		return 0
	}
	return math.Float64frombits(r)
}

func (i *instance) undefined() uint32 {
	r, _ := i.call("qjs_get_undefined")
	return uint32(r)
}

func (i *instance) null() uint32 {
	r, _ := i.call("qjs_get_null")
	return uint32(r)
}

func (i *instance) trueValue() uint32 {
	r, _ := i.call("qjs_get_true")
	return uint32(r)
}

func (i *instance) falseValue() uint32 {
	r, _ := i.call("qjs_get_false")
	return uint32(r)
}

func (i *instance) newNumber(f float64) (uint32, error) {
	r, err := i.call("qjs_new_number", math.Float64bits(f))
	return uint32(r), err
}

func (i *instance) freeValue(h uint32) {
	if h != 0 {
		_, _ = i.callCtx(context.Background(), "qjs_free_value", uint64(h))
	}
}

func (i *instance) eval(code, filename string) (uint32, error) {
	codePtr, err := i.writeCString(code)
	if err != nil {
		return 0, err
	}
	defer i.free(codePtr)
	namePtr, err := i.writeCString(filename)
	if err != nil {
		return 0, err
	}
	defer i.free(namePtr)
	r, err := i.call("qjs_eval", uint64(codePtr), uint64(len(code)), uint64(namePtr), 0)
	if err != nil {
		return 0, err
	}
	return uint32(r), nil
}

func (i *instance) callFunction(fn, this uint32, args ...uint32) (uint32, error) {
	var argvPtr uint32
	if len(args) > 0 {
		p, err := i.malloc(uint64(len(args) * 4))
		if err != nil {
			return 0, err
		}
		argvPtr = p
		defer i.free(argvPtr)
		for idx, arg := range args {
			i.mem.WriteUint32Le(argvPtr+uint32(idx*4), arg)
		}
	}
	r, err := i.call("qjs_call", uint64(fn), uint64(this), uint64(len(args)), uint64(argvPtr))
	if err != nil {
		return 0, err
	}
	return uint32(r), nil
}

func (i *instance) getPropString(obj uint32, name string) (uint32, error) {
	namePtr, err := i.writeCString(name)
	if err != nil {
		return 0, err
	}
	defer i.free(namePtr)
	r, err := i.call("qjs_get_prop_string", uint64(obj), uint64(namePtr))
	if err != nil {
		return 0, err
	}
	return uint32(r), nil
}

func (i *instance) newHostFunction(name string) (uint32, error) {
	namePtr, err := i.writeCString(name)
	if err != nil {
		return 0, err
	}
	defer i.free(namePtr)
	r, err := i.call("qjs_new_host_function", uint64(namePtr), uint64(len(name)), 0)
	if err != nil {
		return 0, err
	}
	return uint32(r), nil
}

func (i *instance) isJobPending() bool {
	r, err := i.call("qjs_is_job_pending")
	return err == nil && r != 0
}

func (i *instance) executePendingJob() (int32, error) {
	r, err := i.call("qjs_execute_pending_job")
	if err != nil {
		return 0, err
	}
	return int32(r), nil
}

func (i *instance) currentException() (*Error, error) {
	h, err := i.call("qjs_get_exception")
	if err != nil {
		return nil, err
	}
	if h == 0 {
		return &Error{Kind: ErrorScript, Name: "Error", Message: "script threw without an exception"}, nil
	}
	defer i.freeValue(uint32(h))
	return i.exceptionFromHandle(uint32(h))
}

func (i *instance) exceptionFromHandle(h uint32) (*Error, error) {
	read := func(key string) (string, bool) {
		p, err := i.getPropString(h, key)
		if err != nil {
			return "", false
		}
		defer i.freeValue(p)
		if i.isUndefined(p) {
			return "", false
		}
		s, err := i.readString(p)
		return s, err == nil
	}
	name, _ := read("name")
	message, _ := read("message")
	stack, _ := read("stack")
	if name == "" {
		name = "Error"
	}
	return &Error{Kind: ErrorScript, Name: name, Message: message, Stack: stack}, nil
}

func (i *instance) newInstanceError(err error) *Error {
	return &Error{Kind: ErrorSandbox, Message: fmt.Sprint(err)}
}
