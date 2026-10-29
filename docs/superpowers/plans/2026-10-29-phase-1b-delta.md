# Phase 1B Chord Delta Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 交付 `harness/delta`：Chord 增量操作模型、校验、可变/不可变应用、线上编解码与保守但正确的差异生成器，为存储协议提供文档增量基础。

**Architecture:** 操作以 Go `any` JSON 树（encoding/json 的 nil/bool/float64/string/[]any/map[string]any，数字用 UseNumber 解码）为值模型。解码后的 Op 与线上 WireOp 是两种独立类型，各带独立校验。Apply 精确实现全部七种动词，codec 实现“第二次使用才建字典、Replace 重置、短形式仅限单批”的协议。DiffRevisions 生成合法且值等价的增量，不追求与 TypeScript 逐字节一致。

**Tech Stack:** Go 1.25.0+ 标准库。语义参考固定 TS 基线 `e65b3b88c08412bb8ffbd21fb5dc5756064a6e5a` 与 MIT 许可的 sky-valley/pi `chord/delta`（测试向量来源，非运行依赖）。

## Global Constraints

- 完整移植 pi-durable 的能力和行为语义，采用 Go 风格 API，运行时不依赖 Node.js。
- 上游提交固定为 `e65b3b88c08412bb8ffbd21fb5dc5756064a6e5a`。
- 本地 module 为 `harness`，`go.mod` 的 go 指令为 `1.25.0`；本子项目不添加第三方依赖。
- 跨语言支持声明只覆盖已验证的格式、版本、任务类型、VM 条件和宿主。
- 保留未知数据与不支持版本的失败行为有测试，不做静默损坏或有损降级。
- 操作序列是“精确但非规范”的：依赖结果值，不依赖精确元组。线上格式与 Apply 语义必须与 TypeScript 一致；DiffRevisions 的输出只要求合法且应用后值等价。

---

## 阅读顺序与执行环境

先读 `/Users/gao/harness-codex/docs/superpowers/specs/2026-10-29-phase-1-compatibility-design.md`（尤其第 4 节记录与增量约束），再读固定基线的 `packages/chord/src/delta/index.ts` 与 `README.md`。上游参考 checkout 在 `/tmp/pi-durable-reference.0YSJx0/upstream`；若缺失，执行时重新取得并校验 SHA，不能退化为最新 main。

任务严格按 1→2→3→4→5 顺序执行。每项先写失败测试并观察 RED，再写最小实现到 GREEN，运行全套后提交。`git diff --check` 与 `go test ./... -count=1` 在每次提交前必须干净。

## 文件布局

| 文件 | 职责 |
|---|---|
| `delta/path.go` | Path/Key/Index/PathID、路径安全、数字与错误类型 |
| `delta/value.go` | JSON 值语义：相等、数字、对象键序、深拷贝 |
| `delta/op.go` | Op/WireOp 模型、校验与 JSON 编解码 |
| `delta/apply.go` | 可变 Apply 与全部七种动词语义 |
| `delta/applyimm.go` | 不可变 Apply、批量应用与副本所有权 |
| `delta/codec.go` | Encoder/Decoder：路径字典、短形式与 Replace 重置 |
| `delta/diff.go` | DiffRevisions 保守生成器 |
| 对应 `*_test.go` | 单元测试 |
| `delta/testdata/delta-vectors.json` | 与 TypeScript 共用的参考向量 |
| `tools/compat/check-delta.mjs` | 开发期 Node.js 对照脚本 |

## 任务与接口

各任务的 Produces 接口在任务中完整定义；后续任务只消费已定义符号。Go 代码为实施依据，执行时按 TDD 创建文件并验证。

### Task 1: Path 与值语义

**Files:** Create `delta/path.go`, `delta/value.go`; Test `delta/path_test.go`, `delta/value_test.go`.

**Interfaces:**
- Produces: `type Seg interface{ seg() }`; `type Key string`; `type Index int64`; `type Path []Seg`; `type PathID int64`; `type PathRef interface{ pathRef() }`; `func (Path) pathRef()`; `func (PathID) pathRef()`; `(Path).String() string`; `(Path).Validate() error`; `(Path).MarshalJSON` / `UnmarshalJSON`; `ReservedSegments map[string]bool`; `type UnsafePathError struct{ Segment Seg }`; `type PathError struct{ Ref PathRef }`; `ErrInvalidOp error`; `maxSafeInteger` 包内常量; `marshalJSON(v any)([]byte,error)` 包内函数; `jsonEqual(a,b any) bool`; `deepCopy(v any) any`; `number(v any)(float64,bool)`; `keyOrder(m map[string]any) []string`。
- Consumes: Go 标准库。

- [ ] **Step 1: 写失败测试**

`delta/path_test.go`:

```go
package delta

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestPathValidate(t *testing.T) {
	ok := Path{Key("a"), Index(0), Key("b")}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid path: %v", err)
	}
	for _, p := range []Path{
		{Key("__proto__")},
		{Key("constructor")},
		{Key("prototype")},
		{Index(-1)},
		{Index(1 << 53)},
	} {
		var unsafe *UnsafePathError
		if !errors.As(p.Validate(), &unsafe) {
			t.Fatalf("path %s: got %v, want UnsafePathError", p, p.Validate())
		}
	}
}

func TestPathJSON(t *testing.T) {
	p := Path{Key("a"), Index(2)}
	raw, err := json.Marshal(p)
	if err != nil || string(raw) != `["a",2]` {
		t.Fatalf("marshal %s %v", raw, err)
	}
	var back Path
	if err := json.Unmarshal(raw, &back); err != nil || back.String() != p.String() {
		t.Fatalf("unmarshal %v %v", back, err)
	}
	if string(mustJSON(Path(nil))) != "[]" {
		t.Fatal("nil path must marshal as []")
	}
	if err := json.Unmarshal([]byte(`[3.5]`), &back); err == nil {
		t.Fatal("non-integer segment must fail")
	}
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
```

`delta/value_test.go`:

```go
package delta

import "testing"

func TestJSONEqualAndKeyOrder(t *testing.T) {
	a := map[string]any{"b": 1.0, "10": "x", "2": "y"}
	b := map[string]any{"2": "y", "10": "x", "b": 1.0}
	if !jsonEqual(a, b) {
		t.Fatal("equal objects with different insertion order")
	}
	if jsonEqual(a, map[string]any{"b": 2.0, "10": "x", "2": "y"}) {
		t.Fatal("differing scalar must not be equal")
	}
	got := keyOrder(a)
	want := []string{"2", "10", "b"}
	if len(got) != len(want) {
		t.Fatalf("key order %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("key order %v, want %v", got, want)
		}
	}
}

func TestDeepCopyDoesNotAlias(t *testing.T) {
	in := map[string]any{"xs": []any{1.0}}
	out := deepCopy(in).(map[string]any)
	out["xs"].([]any)[0] = 9.0
	if in["xs"].([]any)[0] != 1.0 {
		t.Fatal("deep copy aliases input")
	}
}
```

- [ ] **Step 2: 验证 RED**

Run: `go test ./delta -count=1`

Expected: FAIL，delta 包尚无非测试文件。

- [ ] **Step 3: 添加实现**

`delta/path.go`:

```go
package delta

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
)

var ErrInvalidOp = errors.New("invalid op")

type Seg interface{ seg() }
type Key string
type Index int64

func (Key) seg()   {}
func (Index) seg() {}

func (k Key) String() string   { return string(k) }
func (i Index) String() string { return strconv.FormatInt(int64(i), 10) }

func (k Key) MarshalJSON() ([]byte, error) { return marshalJSON(string(k)) }
func (i Index) MarshalJSON() ([]byte, error) {
	return strconv.AppendInt(nil, int64(i), 10), nil
}

type Path []Seg
type PathID int64
type PathRef interface{ pathRef() }

func (Path) pathRef()   {}
func (PathID) pathRef() {}

func (p Path) MarshalJSON() ([]byte, error) {
	if p == nil {
		return []byte("[]"), nil
	}
	return marshalJSON([]Seg(p))
}

func (p *Path) UnmarshalJSON(data []byte) error {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	parsed, err := parsePath(v)
	if err != nil {
		return err
	}
	*p = parsed
	return nil
}

func (p Path) String() string {
	data, _ := p.MarshalJSON()
	return string(data)
}

func (p Path) Validate() error {
	for _, seg := range p {
		switch s := seg.(type) {
		case Key:
			if ReservedSegments[string(s)] {
				return &UnsafePathError{Segment: s}
			}
		case Index:
			if s < 0 || s > maxSafeInteger {
				return &UnsafePathError{Segment: s}
			}
		}
	}
	return nil
}

func parsePath(v any) (Path, error) {
	if p, ok := v.(Path); ok {
		return p, nil
	}
	raw, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%w: path is not an array", ErrInvalidOp)
	}
	path := make(Path, 0, len(raw))
	for _, elem := range raw {
		seg, err := parseSeg(elem)
		if err != nil {
			return nil, err
		}
		path = append(path, seg)
	}
	return path, nil
}

func parseSeg(v any) (Seg, error) {
	switch s := v.(type) {
	case Seg:
		return s, nil
	case string:
		return Key(s), nil
	}
	if n, ok := integer(v); ok {
		return Index(n), nil
	}
	return nil, fmt.Errorf("%w: path segment is neither key nor index", ErrInvalidOp)
}

var ReservedSegments = map[string]bool{
	"__proto__":   true,
	"constructor": true,
	"prototype":   true,
}

type UnsafePathError struct{ Segment Seg }

func (e *UnsafePathError) Error() string {
	return fmt.Sprintf("unsafe path segment: %s", e.Segment)
}

type PathError struct{ Ref PathRef }

func (e *PathError) Error() string {
	return "unresolvable path: " + refString(e.Ref)
}

func refString(ref PathRef) string {
	switch r := ref.(type) {
	case Path:
		return r.String()
	case PathID:
		return strconv.FormatInt(int64(r), 10)
	default:
		return Path(nil).String()
	}
}

const maxSafeInteger = 1<<53 - 1

func integer(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case int32:
		return int64(n), true
	case float64:
		return floatInteger(n)
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return i, true
		}
		if f, err := n.Float64(); err == nil {
			return floatInteger(f)
		}
	}
	return 0, false
}

func floatInteger(f float64) (int64, bool) {
	if f != math.Trunc(f) || math.IsInf(f, 0) {
		return 0, false
	}
	if f >= 1<<63 {
		return math.MaxInt64, true
	}
	if f <= -1<<63 {
		return math.MinInt64, true
	}
	return int64(f), true
}

func marshalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
```

`delta/value.go`:

```go
package delta

import (
	"cmp"
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strconv"
)

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(rv.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return float64(rv.Uint()), true
	case reflect.Float32, reflect.Float64:
		return rv.Float(), true
	}
	return 0, false
}

func jsonEqual(a, b any) bool {
	fa, oka := number(a)
	fb, okb := number(b)
	if oka && okb {
		return fa == fb
	}
	if oka != okb {
		return false
	}
	switch x := a.(type) {
	case nil:
		return b == nil
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case string:
		y, ok := b.(string)
		return ok && x == y
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !jsonEqual(x[i], y[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, v := range x {
			w, ok := y[k]
			if !ok || !jsonEqual(v, w) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(a, b)
}

func deepCopy(v any) any {
	switch x := v.(type) {
	case nil, bool, string, json.Number:
		return x
	case float64:
		return x
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = deepCopy(x[i])
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = deepCopy(val)
		}
		return out
	default:
		return v
	}
}

func keyOrder(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, compareKeys)
	return keys
}

func compareKeys(a, b string) int {
	ia, oka := canonicalIndex(a)
	ib, okb := canonicalIndex(b)
	switch {
	case oka && okb:
		return cmp.Compare(ia, ib)
	case oka:
		return -1
	case okb:
		return 1
	}
	ua, ub := utf16Units(a), utf16Units(b)
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			if ua[i] < ub[i] {
				return -1
			}
			return 1
		}
	}
	return cmp.Compare(len(ua), len(ub))
}

func canonicalIndex(s string) (int64, bool) {
	if s == "" || len(s) > 10 || s[0] < '0' || s[0] > '9' || s[0] == '0' && len(s) > 1 {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n > 1<<32-2 {
		return 0, false
	}
	return n, true
}

func utf16Units(s string) []uint16 {
	out := make([]uint16, 0, len(s))
	for _, r := range s {
		if r <= 0xffff {
			out = append(out, uint16(r))
		} else {
			hi, lo := utf16Encode(r)
			out = append(out, uint16(hi), uint16(lo))
		}
	}
	return out
}
```

`utf16Encode` 在 Task 3 的 `apply.go` 中给出（包内小函数），避免重复。

- [ ] **Step 4: 验证 GREEN**

Run: `go test ./delta -count=1`

Expected: 两个测试文件全部 PASS（注意 `apply.go` 尚未存在，`utf16Encode` 引用会失败——因此 Step 3 同时需要创建最小 `apply.go` 占位函数）。为此在本任务提交前，`apply.go` 先只包含 `utf16Encode`：

```go
package delta

import "unicode/utf16"

func utf16Encode(r rune) (uint16, uint16) {
	return utf16.EncodeRune(r)
}
```

- [ ] **Step 5: 格式化、复验并提交**

Run: `gofmt -w delta/*.go && go test ./delta -count=1 && git diff --check`

Run: `git add -- delta/path.go delta/value.go delta/path_test.go delta/value_test.go delta/apply.go`

Run: `git commit -m "feat(delta): add paths, safety and JSON value semantics"`

### Task 2: Op 模型与校验

**Files:** Create `delta/op.go`; Test `delta/op_test.go`.

**Interfaces:**
- Produces: `type Op interface{ tuple; op() }`; `type Replace struct{ Value any }`; `Set`, `Delete`, `Append`, `Truncate`, `Splice`, `Permute`；`type WireOp interface{ tuple; wireOp() }`; `WireSet/WireDelete/WireAppend/WireTruncate/WireSplice/WirePermute/Define`；`ParseOp(any)(Op,error)`；`ParseWireOp(any)(WireOp,error)`；`UnmarshalOp/UnmarshalWireOp`；每个类型的 `MarshalJSON`；`IsReplace`, `IsBase`。
- Consumes: Task 1 的 Path/PathRef/PathID/错误类型与 `marshalJSON`/`integer`。

- [ ] **Step 1: 写失败测试**

`delta/op_test.go`:

```go
package delta

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestParseOpShapes(t *testing.T) {
	cases := []struct {
		raw string
		ok  bool
	}{
		{`["r",{"a":1}]`, true},
		{`["s",["a"],1]`, true},
		{`["d",["a"]]`, true},
		{`["a",["a"],"x"]`, true},
		{`["t",["a"],1]`, true},
		{`["p",["a"],0,1,[2]]`, true},
		{`["m",["a"],[1,0]]`, true},
		{`["s"]`, false},
		{`["x",["a"],1]`, false},
		{`["s",["__proto__"],1]`, false},
		{`["p",["a"],-1,0,[]]`, false},
		{`["m",["a"],[1,1]]`, false},
	}
	for _, tc := range cases {
		var v any
		if err := json.Unmarshal([]byte(tc.raw), &v); err != nil {
			t.Fatal(err)
		}
		_, err := ParseOp(v)
		if (err == nil) != tc.ok {
			t.Fatalf("%s: err=%v want ok=%v", tc.raw, err, tc.ok)
		}
	}
}

func TestWireShortFormAndDictionary(t *testing.T) {
	enc := Encoder{}
	b1 := enc.Encode([]Op{
		Set{Path: Path{Key("a")}, Value: 1.0},
		Set{Path: Path{Key("a")}, Value: 2.0},
	})
	b2 := enc.Encode([]Op{Set{Path: Path{Key("a")}, Value: 3.0}})
	var dec Decoder
	ops := dec.Decode(b1)
	if len(ops) != 2 || ops[1].(Set).Path.String() != `["a"]` {
		t.Fatalf("first batch decode: %#v", ops)
	}
	ops = dec.Decode(b2)
	if len(ops) != 1 || ops[0].(Set).Path.String() != `["a"]` {
		t.Fatalf("second batch did not reuse id: %#v", ops)
	}
	if len(b1) != 3 {
		t.Fatalf("expected inline, definition, reference: %#v", b1)
	}
}

func TestIsBase(t *testing.T) {
	if !IsBase([]Op{Replace{Value: 1.0}}) {
		t.Fatal("replace must be a base")
	}
	if IsBase([]Op{Set{Path: Path{Key("a")}, Value: 1.0}}) {
		t.Fatal("set must not be a base")
	}
	if !errors.Is(ParseOp(mustAny(`["x",1]`)), ErrInvalidOp) {
		t.Fatal("unknown verb must wrap ErrInvalidOp")
	}
}

func mustAny(raw string) any {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		panic(err)
	}
	return v
}
```

- [ ] **Step 2: 验证 RED**

Run: `go test ./delta -run 'TestParseOpShapes|TestWireShortFormAndDictionary|TestIsBase' -count=1`

Expected: FAIL，Op/WireOp/Encoder/Decoder 未定义。

- [ ] **Step 3: 添加完整实现**

`delta/op.go`:

```go
package delta

import (
	"encoding/json"
	"errors"
	"fmt"
)

type tuple interface {
	json.Marshaler
	Validate() error
}

type Op interface {
	tuple
	op()
}

type Replace struct{ Value any }
type Set struct {
	Path  Path
	Value any
}
type Delete struct{ Path Path }
type Append struct {
	Path Path
	Text string
}
type Truncate struct {
	Path  Path
	Count int
}
type Splice struct {
	Path   Path
	Index  int
	Remove int
	Items  []any
}
type Permute struct {
	Path        Path
	Permutation []int
}

func (Replace) op()  {}
func (Set) op()      {}
func (Delete) op()   {}
func (Append) op()   {}
func (Truncate) op() {}
func (Splice) op()   {}
func (Permute) op()  {}

type WireOp interface {
	tuple
	wireOp()
}

type WireSet struct {
	Ref   PathRef
	Value any
}
type WireDelete struct{ Ref PathRef }
type WireAppend struct {
	Ref  PathRef
	Text string
}
type WireTruncate struct {
	Ref   PathRef
	Count int
}
type WireSplice struct {
	Ref    PathRef
	Index  int
	Remove int
	Items  []any
}
type WirePermute struct {
	Ref         PathRef
	Permutation []int
}
type Define struct {
	ID   PathID
	Path Path
}

func (Replace) wireOp()      {}
func (WireSet) wireOp()      {}
func (WireDelete) wireOp()   {}
func (WireAppend) wireOp()   {}
func (WireTruncate) wireOp() {}
func (WireSplice) wireOp()   {}
func (WirePermute) wireOp()  {}
func (Define) wireOp()       {}

func IsReplace[O tuple](op O) bool {
	switch any(op).(type) {
	case Replace, *Replace:
		return true
	}
	return false
}

func IsBase[O tuple](ops []O) bool {
	return len(ops) > 0 && IsReplace(ops[0])
}

func ParseOp(v any) (Op, error) {
	t, verb, err := tupleOf(v)
	if err != nil {
		return nil, err
	}
	var op Op
	switch verb {
	case "r":
		if err := arity(t, 2, `["r", value]`); err != nil {
			return nil, err
		}
		op = Replace{Value: t[1]}
	case "s":
		if err := arity(t, 3, `["s", path, value]`); err != nil {
			return nil, err
		}
		path, err := parsePath(t[1])
		if err != nil {
			return nil, err
		}
		op = Set{Path: path, Value: t[2]}
	case "d":
		if err := arity(t, 2, `["d", path]`); err != nil {
			return nil, err
		}
		path, err := parsePath(t[1])
		if err != nil {
			return nil, err
		}
		op = Delete{Path: path}
	case "a":
		if err := arity(t, 3, `["a", path, text]`); err != nil {
			return nil, err
		}
		path, err := parsePath(t[1])
		if err != nil {
			return nil, err
		}
		s, ok := t[2].(string)
		if !ok {
			return nil, fmt.Errorf("%w: \"a\" text must be a string", ErrInvalidOp)
		}
		op = Append{Path: path, Text: s}
	case "t":
		if err := arity(t, 3, `["t", path, count]`); err != nil {
			return nil, err
		}
		path, err := parsePath(t[1])
		if err != nil {
			return nil, err
		}
		n, err := parseCount(t[2])
		if err != nil {
			return nil, err
		}
		op = Truncate{Path: path, Count: n}
	case "p":
		if err := arity(t, 5, `["p", path, index, remove, items]`); err != nil {
			return nil, err
		}
		path, err := parsePath(t[1])
		if err != nil {
			return nil, err
		}
		index, remove, items, err := spliceArgs(t[2], t[3], t[4])
		if err != nil {
			return nil, err
		}
		op = Splice{Path: path, Index: index, Remove: remove, Items: items}
	case "m":
		if err := arity(t, 3, `["m", path, permutation]`); err != nil {
			return nil, err
		}
		path, err := parsePath(t[1])
		if err != nil {
			return nil, err
		}
		permutation, err := parsePermutation(t[2])
		if err != nil {
			return nil, err
		}
		op = Permute{Path: path, Permutation: permutation}
	default:
		return nil, fmt.Errorf("%w: unknown op verb %q", ErrInvalidOp, verb)
	}
	if err := op.Validate(); err != nil {
		return nil, err
	}
	return op, nil
}

func ParseWireOp(v any) (WireOp, error) {
	t, verb, err := tupleOf(v)
	if err != nil {
		return nil, err
	}
	var op WireOp
	switch verb {
	case "r":
		if err := arity(t, 2, `["r", value]`); err != nil {
			return nil, err
		}
		op = Replace{Value: t[1]}
	case "s":
		ref, rest, err := refArgs(t, 1)
		if err != nil {
			return nil, err
		}
		op = WireSet{Ref: ref, Value: rest[0]}
	case "d":
		ref, _, err := refArgs(t, 0)
		if err != nil {
			return nil, err
		}
		op = WireDelete{Ref: ref}
	case "a":
		ref, rest, err := refArgs(t, 1)
		if err != nil {
			return nil, err
		}
		s, ok := rest[0].(string)
		if !ok {
			return nil, fmt.Errorf("%w: \"a\" text must be a string", ErrInvalidOp)
		}
		op = WireAppend{Ref: ref, Text: s}
	case "t":
		ref, rest, err := refArgs(t, 1)
		if err != nil {
			return nil, err
		}
		n, err := parseCount(rest[0])
		if err != nil {
			return nil, err
		}
		op = WireTruncate{Ref: ref, Count: n}
	case "p":
		ref, rest, err := refArgs(t, 3)
		if err != nil {
			return nil, err
		}
		index, remove, items, err := spliceArgs(rest[0], rest[1], rest[2])
		if err != nil {
			return nil, err
		}
		op = WireSplice{Ref: ref, Index: index, Remove: remove, Items: items}
	case "m":
		ref, rest, err := refArgs(t, 1)
		if err != nil {
			return nil, err
		}
		permutation, err := parsePermutation(rest[0])
		if err != nil {
			return nil, err
		}
		op = WirePermute{Ref: ref, Permutation: permutation}
	case "#":
		if err := arity(t, 3, `["#", id, path]`); err != nil {
			return nil, err
		}
		id, ok := integer(t[1])
		if !ok {
			return nil, fmt.Errorf("%w: \"#\" id must be a non-negative integer", ErrInvalidOp)
		}
		path, err := parsePath(t[2])
		if err != nil {
			return nil, err
		}
		op = Define{ID: PathID(id), Path: path}
	default:
		return nil, fmt.Errorf("%w: unknown wire op verb %q", ErrInvalidOp, verb)
	}
	if err := op.Validate(); err != nil {
		return nil, err
	}
	return op, nil
}

func UnmarshalOp(data []byte) (Op, error) {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return ParseOp(v)
}

func UnmarshalWireOp(data []byte) (WireOp, error) {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return ParseWireOp(v)
}

func tupleOf(v any) ([]any, string, error) {
	t, ok := v.([]any)
	if !ok || len(t) == 0 {
		return nil, "", fmt.Errorf("%w: op is not a tuple", ErrInvalidOp)
	}
	verb, _ := t[0].(string)
	return t, verb, nil
}

func arity(t []any, want int, shape string) error {
	if len(t) != want {
		return fmt.Errorf("%w: op expects %s (%d elements), got %d", ErrInvalidOp, shape, want, len(t))
	}
	return nil
}

func refArgs(t []any, n int) (PathRef, []any, error) {
	switch len(t) {
	case 1 + n:
		return nil, t[1:], nil
	case 2 + n:
		ref, err := parseRef(t[1])
		if err != nil {
			return nil, nil, err
		}
		return ref, t[2:], nil
	default:
		return nil, nil, fmt.Errorf("%w: bad wire op arity %d", ErrInvalidOp, len(t))
	}
}

func parseRef(v any) (PathRef, error) {
	if id, ok := integer(v); ok {
		return PathID(id), nil
	}
	return parsePath(v)
}

func parseCount(v any) (int, error) {
	n, ok := integer(v)
	if !ok {
		return 0, fmt.Errorf("%w: count must be a non-negative integer", ErrInvalidOp)
	}
	if n < 0 || n > 1<<30 {
		return 0, fmt.Errorf("%w: count out of range", ErrInvalidOp)
	}
	return int(n), nil
}

func spliceArgs(i, r, itemsArg any) (int, int, []any, error) {
	index, err := parseCount(i)
	if err != nil {
		return 0, 0, nil, err
	}
	remove, err := parseCount(r)
	if err != nil {
		return 0, 0, nil, err
	}
	items, ok := itemsArg.([]any)
	if !ok {
		return 0, 0, nil, fmt.Errorf("%w: \"p\" items must be an array", ErrInvalidOp)
	}
	return index, remove, items, nil
}

func parsePermutation(v any) ([]int, error) {
	xs, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%w: \"m\" permutation is not an array", ErrInvalidOp)
	}
	out := make([]int, len(xs))
	for i, x := range xs {
		n, ok := integer(x)
		if !ok {
			return nil, fmt.Errorf("%w: permutation is not a bijection", ErrInvalidOp)
		}
		out[i] = int(n)
	}
	if err := bijection(out); err != nil {
		return nil, err
	}
	return out, nil
}

func bijection(permutation []int) error {
	seen := make([]bool, len(permutation))
	for i, n := range permutation {
		if n < 0 || n >= len(permutation) || seen[n] {
			return fmt.Errorf("%w: permutation is not a bijection", ErrInvalidOp)
		}
		seen[n] = true
	}
	return nil
}

// ─── Validate and marshal per op ──────────────────────────────────────────────

func (Replace) Validate() error { return nil }
func (Set) Validate() error     { return nonEmpty("s", Set{}.Path) }
func (Delete) Validate() error  { return nonEmpty("d", Delete{}.Path) }
func (Append) Validate() error  { return nonEmpty("a", Append{}.Path) }
func (Truncate) Validate() error {
	if Truncate{}.Count < 0 {
		return fmt.Errorf("%w: \"t\" count must be non-negative", ErrInvalidOp)
	}
	return nonEmpty("t", Truncate{}.Path)
}
func (Splice) Validate() error {
	if Splice{}.Index < 0 || Splice{}.Remove < 0 {
		return fmt.Errorf("%w: \"p\" index and remove must be non-negative", ErrInvalidOp)
	}
	return Splice{}.Path.Validate()
}
func (Permute) Validate() error {
	if err := bijection(Permute{}.Permutation); err != nil {
		return err
	}
	return Permute{}.Path.Validate()
}

func (Define) Validate() error {
	if Define{}.ID < 0 || Define{}.ID > maxSafeInteger {
		return fmt.Errorf("%w: path id out of range", ErrInvalidOp)
	}
	return Define{}.Path.Validate()
}
func (WireSet) Validate() error      { return validateRef(WireSet{}.Ref) }
func (WireDelete) Validate() error   { return validateRef(WireDelete{}.Ref) }
func (WireAppend) Validate() error   { return validateRef(WireAppend{}.Ref) }
func (WireTruncate) Validate() error {
	if WireTruncate{}.Count < 0 {
		return fmt.Errorf("%w: \"t\" count must be non-negative", ErrInvalidOp)
	}
	return validateRef(WireTruncate{}.Ref)
}
func (WireSplice) Validate() error {
	if WireSplice{}.Index < 0 || WireSplice{}.Remove < 0 {
		return fmt.Errorf("%w: \"p\" index and remove must be non-negative", ErrInvalidOp)
	}
	return validateRef(WireSplice{}.Ref)
}
func (WirePermute) Validate() error {
	if err := bijection(WirePermute{}.Permutation); err != nil {
		return err
	}
	return validateRef(WirePermute{}.Ref)
}

func validateRef(ref PathRef) error {
	switch r := ref.(type) {
	case nil:
		return nil
	case Path:
		return r.Validate()
	case PathID:
		if r < 0 || r > maxSafeInteger {
			return fmt.Errorf("%w: path id out of range", ErrInvalidOp)
		}
		return nil
	}
	return fmt.Errorf("%w: bad path ref", ErrInvalidOp)
}

func nonEmpty(verb string, p Path) error {
	if len(p) == 0 {
		return fmt.Errorf("%w: %q path is empty; only \"r\" replaces the root", ErrInvalidOp, verb)
	}
	return p.Validate()
}

func (o Replace) MarshalJSON() ([]byte, error) { return marshalJSON([]any{"r", o.Value}) }
func (o Set) MarshalJSON() ([]byte, error) {
	return marshalJSON([]any{"s", o.Path, o.Value})
}
func (o Delete) MarshalJSON() ([]byte, error) {
	return marshalJSON([]any{"d", o.Path})
}
func (o Append) MarshalJSON() ([]byte, error) {
	return marshalJSON([]any{"a", o.Path, o.Text})
}
func (o Truncate) MarshalJSON() ([]byte, error) {
	return marshalJSON([]any{"t", o.Path, o.Count})
}
func (o Splice) MarshalJSON() ([]byte, error) {
	return marshalJSON([]any{"p", o.Path, o.Index, o.Remove, nonNilItems(o.Items)})
}
func (o Permute) MarshalJSON() ([]byte, error) {
	return marshalJSON([]any{"m", o.Path, nonNilPermutation(o.Permutation)})
}

func (o Define) MarshalJSON() ([]byte, error) {
	return marshalJSON([]any{"#", o.ID, o.Path})
}
func (o WireSet) MarshalJSON() ([]byte, error) {
	return marshalWire("s", o.Ref, o.Value)
}
func (o WireDelete) MarshalJSON() ([]byte, error) {
	return marshalWire("d", o.Ref)
}
func (o WireAppend) MarshalJSON() ([]byte, error) {
	return marshalWire("a", o.Ref, o.Text)
}
func (o WireTruncate) MarshalJSON() ([]byte, error) {
	return marshalWire("t", o.Ref, o.Count)
}
func (o WireSplice) MarshalJSON() ([]byte, error) {
	return marshalWire("p", o.Ref, o.Index, o.Remove, nonNilItems(o.Items))
}
func (o WirePermute) MarshalJSON() ([]byte, error) {
	return marshalWire("m", o.Ref, nonNilPermutation(o.Permutation))
}

func marshalWire(verb string, ref PathRef, rest ...any) ([]byte, error) {
	out := make([]any, 0, 2+len(rest))
	out = append(out, verb)
	if ref != nil {
		out = append(out, ref)
	}
	return marshalJSON(append(out, rest...))
}

func nonNilItems(v []any) []any {
	if v == nil {
		return []any{}
	}
	return v
}

func nonNilPermutation(v []int) []int {
	if v == nil {
		return []int{}
	}
	return v
}
```

- [ ] **Step 4: 验证 GREEN**

Run: `go test ./delta -count=1`

Expected: 全部 PASS。注意 `TestWireShortFormAndDictionary` 依赖 Task 4 的 Encoder/Decoder；为满足本任务独立可测，Task 2 提交前先创建最小 `delta/codec.go`（Task 4 的完整实现），见 Task 4。执行顺序上把 Task 4 的 codec 与 Task 2 一并提交亦可，但保持 RED/GREEN 证据按任务记录。

- [ ] **Step 5: 格式化、复验并提交**

Run: `gofmt -w delta/*.go && go test ./delta -count=1 && git diff --check`

Run: `git add -- delta/op.go delta/op_test.go`

Run: `git commit -m "feat(delta): add operation model and validation"`

### Task 3: 可变 Apply

**Files:** Modify `delta/apply.go`; Test `delta/apply_test.go`.

**Interfaces:**
- Produces: `Apply(root any, ops []Op) (any, error)`；包内 `applyOps`, `applyOne`, `walk`, `atParent`, `write`, `remove`, `splice`, `truncateUTF16`, `appendCopy`, `propertyKey`, `arrayIndex`。
- Consumes: Task 1/2 类型与 `jsonEqual`/`deepCopy`/`number`。

- [ ] **Step 1: 写失败测试**

`delta/apply_test.go`:

```go
package delta

import (
	"encoding/json"
	"errors"
	"testing"
)

func tree(t *testing.T, raw string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func ops(t *testing.T, raw string) []Op {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatal(err)
	}
	batch, ok := v.([]any)
	if !ok {
		t.Fatal("not a batch")
	}
	out := make([]Op, 0, len(batch))
	for _, item := range batch {
		op, err := ParseOp(item)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, op)
	}
	return out
}

func TestApplyVerbs(t *testing.T) {
	root := tree(t, `{"text":"abc","xs":[1,2,3],"obj":{"a":1},"gone":true}`)
	got, err := Apply(root, ops(t, `[
		["a",["text"],"d"],
		["t",["text"],2],
		["p",["xs"],1,1,[9]],
		["s",["obj","b"],2],
		["d",["gone"]]
	]`))
	if err != nil {
		t.Fatal(err)
	}
	want := tree(t, `{"text":"cd","xs":[1,9,3],"obj":{"a":1,"b":2}}`)
	if !jsonEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestApplyTruncateUTF16(t *testing.T) {
	root := tree(t, `{"s":"\uD83D\uDE00x"}`)
	got, err := Apply(root, []Op{Truncate{Path: Path{Key("s")}, Count: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if got.(map[string]any)["s"] != "\uFFFDx" {
		t.Fatalf("surrogate split: %q", got.(map[string]any)["s"])
	}
}

func TestApplyErrors(t *testing.T) {
	var unsafe *UnsafePathError
	var pathErr *PathError
	if _, err := Apply(tree(t, `{"a":1}`), []Op{Set{Path: Path{Key("__proto__")}, Value: 1.0}}); !errors.As(err, &unsafe) {
		t.Fatalf("reserved: %v", err)
	}
	if _, err := Apply(tree(t, `{"xs":[1]}`), []Op{Set{Path: Path{Key("xs"), Index(5)}, Value: 1.0}}); !errors.As(err, &unsafe) {
		t.Fatalf("gap: %v", err)
	}
	if _, err := Apply(tree(t, `{}`), []Op{Set{Path: Path{Key("missing")}, Value: 1.0}}); err == nil {
		t.Fatal("unresolvable parent must error")
	} else if !errors.As(err, &pathErr) {
		t.Fatalf("want PathError: %v", err)
	}
}

func TestSpliceClamps(t *testing.T) {
	got, err := Apply(tree(t, `[1,2,3]`), []Op{Splice{Path: Path{}, Index: 2, Remove: 10, Items: []any{4.0}}})
	if err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(got, []any{1.0, 2.0, 4.0}) {
		t.Fatalf("clamped splice: %v", got)
	}
}
```

- [ ] **Step 2: 验证 RED**

Run: `go test ./delta -run 'TestApply|TestSplice' -count=1`

Expected: FAIL，Apply 未实现（或 apply.go 仅含 utf16Encode 占位）。

- [ ] **Step 3: 添加完整实现**

`delta/apply.go`（替换 Task 1 的占位，保留 `utf16Encode`）:

```go
package delta

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"unicode/utf16"
	"unicode/utf8"
)

func utf16Encode(r rune) (uint16, uint16) { return utf16.EncodeRune(r) }

func Apply(root any, ops []Op) (any, error) {
	return applyOps(root, ops, nil)
}

func applyOps(root any, ops []Op, owned *ownedSet) (any, error) {
	for _, op := range ops {
		if op == nil {
			return nil, fmt.Errorf("%w: op is nil", ErrInvalidOp)
		}
		if err := op.Validate(); err != nil {
			return nil, err
		}
		next, err := applyOne(root, op, owned)
		if err != nil {
			return nil, err
		}
		root = next
	}
	return root, nil
}

func applyOne(root any, op Op, owned *ownedSet) (any, error) {
	switch op := op.(type) {
	case Replace:
		return op.Value, nil
	case Splice:
		return walk(root, op.Path, owned, func(node any) (any, error) {
			xs, ok := node.([]any)
			if !ok {
				return nil, &PathError{Ref: op.Path}
			}
			if owned != nil {
				if appended, ok := appendCopy(xs, op, owned); ok {
					return appended, nil
				}
				xs = owned.claim(xs).([]any)
			}
			return splice(xs, op.Index, op.Remove, op.Items), nil
		})
	case Permute:
		return walk(root, op.Path, owned, func(node any) (any, error) {
			xs, ok := node.([]any)
			if !ok || len(xs) != len(op.Permutation) {
				return nil, &PathError{Ref: op.Path}
			}
			previous := slices.Clone(xs)
			for i, from := range op.Permutation {
				xs[i] = previous[from]
			}
			return xs, nil
		})
	case Set:
		return atParent(root, op.Path, owned, func(parent any, key Seg) (any, error) {
			return write(parent, key, op.Path, func(any) (any, error) { return op.Value, nil })
		})
	case Delete:
		return atParent(root, op.Path, owned, func(parent any, key Seg) (any, error) {
			return remove(parent, key, op.Path)
		})
	case Append:
		return atParent(root, op.Path, owned, func(parent any, key Seg) (any, error) {
			return write(parent, key, op.Path, func(current any) (any, error) {
				s, ok := current.(string)
				if !ok {
					return nil, &PathError{Ref: op.Path}
				}
				return s + op.Text, nil
			})
		})
	case Truncate:
		return atParent(root, op.Path, owned, func(parent any, key Seg) (any, error) {
			return write(parent, key, op.Path, func(current any) (any, error) {
				s, ok := current.(string)
				if !ok {
					return nil, &PathError{Ref: op.Path}
				}
				return truncateUTF16(s, op.Count), nil
			})
		})
	}
	return nil, fmt.Errorf("%w: unknown op %T", ErrInvalidOp, op)
}

func walk(root any, path Path, owned *ownedSet, fn func(node any) (any, error)) (any, error) {
	var step func(node any, rest Path) (any, error)
	step = func(node any, rest Path) (any, error) {
		if len(rest) == 0 {
			next, err := fn(node)
			if err == nil && owned != nil {
				owned.adopt(next)
			}
			return next, err
		}
		if owned != nil {
			node = owned.claim(node)
		}
		seg := rest[0]
		switch c := node.(type) {
		case map[string]any:
			key := propertyKey(seg)
			child, ok := c[key]
			if !ok {
				return nil, &PathError{Ref: path}
			}
			next, err := step(child, rest[1:])
			if err != nil {
				return nil, err
			}
			c[key] = next
			return c, nil
		case []any:
			i, ok := seg.(Index)
			if !ok {
				if owned != nil && !ownsKey(c, seg) {
					return nil, &PathError{Ref: path}
				}
				return nil, &UnsafePathError{Segment: seg}
			}
			if i >= Index(len(c)) {
				return nil, &PathError{Ref: path}
			}
			next, err := step(c[i], rest[1:])
			if err != nil {
				return nil, err
			}
			c[i] = next
			return c, nil
		}
		return nil, &PathError{Ref: path}
	}
	return step(root, path)
}

func atParent(root any, path Path, owned *ownedSet, fn func(parent any, key Seg) (any, error)) (any, error) {
	parentPath := path[:len(path)-1]
	return walk(root, parentPath, owned, func(node any) (any, error) {
		switch node.(type) {
		case map[string]any, []any:
		default:
			return nil, &PathError{Ref: parentPath}
		}
		return fn(node, path[len(path)-1])
	})
}

func write(parent any, key Seg, path Path, fn func(current any) (any, error)) (any, error) {
	switch p := parent.(type) {
	case map[string]any:
		k := propertyKey(key)
		v, err := fn(p[k])
		if err != nil {
			return nil, err
		}
		p[k] = v
		return p, nil
	case []any:
		i, err := arrayIndex(p, key)
		if err != nil {
			return nil, err
		}
		if i == len(p) {
			v, err := fn(nil)
			if err != nil {
				return nil, err
			}
			return append(p, v), nil
		}
		v, err := fn(p[i])
		if err != nil {
			return nil, err
		}
		p[i] = v
		return p, nil
	}
	return nil, &PathError{Ref: path[:len(path)-1]}
}

func remove(parent any, key Seg, path Path) (any, error) {
	switch p := parent.(type) {
	case map[string]any:
		delete(p, propertyKey(key))
		return p, nil
	case []any:
		i, err := arrayIndex(p, key)
		if err != nil {
			return nil, err
		}
		if i >= len(p) {
			return nil, &PathError{Ref: path}
		}
		return slices.Delete(p, i, i+1), nil
	}
	return nil, &PathError{Ref: path[:len(path)-1]}
}

func propertyKey(seg Seg) string {
	switch s := seg.(type) {
	case Key:
		return string(s)
	case Index:
		return s.String()
	}
	return fmt.Sprint(seg)
}

func arrayIndex(xs []any, key Seg) (int, error) {
	i, ok := key.(Index)
	if !ok {
		return 0, &UnsafePathError{Segment: key}
	}
	if i > Index(len(xs)) {
		return 0, &UnsafePathError{Segment: key}
	}
	return int(i), nil
}

func splice(xs []any, index, remove int, items []any) []any {
	index = min(index, len(xs))
	remove = min(remove, len(xs)-index)
	xs = slices.Delete(xs, index, index+remove)
	return slices.Insert(xs, index, items...)
}

func truncateUTF16(s string, count int) string {
	i := 0
	for count > 0 && i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		if utf16.RuneLen(r) == 2 && count == 1 {
			return "\uFFFD" + s[i+size:]
		}
		count -= utf16.RuneLen(r)
		i += size
	}
	return s[i:]
}

func appendCopy(xs []any, op Splice, owned *ownedSet) ([]any, bool) {
	if op.Remove != 0 || op.Index != len(xs) || owned.owns(xs) {
		return nil, false
	}
	appended := make([]any, 0, max(len(xs)+len(op.Items), 1))
	return append(append(appended, xs...), op.Items...), true
}

func ownsKey(xs []any, key Seg) bool {
	k := propertyKey(key)
	if k == "length" {
		return true
	}
	i, ok := canonicalIndex(k)
	return ok && i < int64(len(xs))
}

type ownedSet struct {
	at   map[uintptr]bool
	keep []any
}

func newOwnedSet() *ownedSet { return &ownedSet{at: map[uintptr]bool{}} }

func (o *ownedSet) owns(v any) bool {
	switch v.(type) {
	case map[string]any, []any:
		return o.at[reflect.ValueOf(v).Pointer()]
	}
	return false
}

func (o *ownedSet) adopt(v any) {
	var ptr uintptr
	switch x := v.(type) {
	case map[string]any:
		if x == nil {
			return
		}
		ptr = reflect.ValueOf(v).Pointer()
	case []any:
		if cap(x) == 0 {
			return
		}
		ptr = reflect.ValueOf(v).Pointer()
	default:
		return
	}
	if !o.at[ptr] {
		o.at[ptr] = true
		o.keep = append(o.keep, v)
	}
}

func (o *ownedSet) claim(v any) any {
	if o.owns(v) {
		return v
	}
	var c any
	switch x := v.(type) {
	case map[string]any:
		if x == nil {
			return v
		}
		c = maps.Clone(x)
	case []any:
		c = slices.Clone(x)
		if cap(c.([]any)) == 0 {
			c = append([]any{}, c.([]any)...)
		}
	default:
		return v
	}
	o.adopt(c)
	return c
}
```

- [ ] **Step 4: 验证 GREEN**

Run: `go test ./delta -count=1`

Expected: 全部 PASS。

- [ ] **Step 5: 格式化、复验并提交**

Run: `gofmt -w delta/*.go && go test ./delta -count=1 && git diff --check`

Run: `git add -- delta/apply.go delta/apply_test.go`

Run: `git commit -m "feat(delta): add mutable apply with exact verb semantics"`

### Task 4: 线上编解码

**Files:** Create `delta/codec.go`; Test `delta/codec_test.go`.

**Interfaces:**
- Produces: `type Encoder struct{...}`; `(e *Encoder) Encode([]Op) []WireOp`; `type Decoder struct{...}`; `(d *Decoder) Decode([]WireOp) []Op`；包内 `opPath`, `toWire`。
- Consumes: Task 1/2 类型。

- [ ] **Step 1: 写失败测试**

`delta/codec_test.go`:

```go
package delta

import (
	"encoding/json"
	"testing"
)

func decodeAll(t *testing.T, wire []WireOp) []Op {
	t.Helper()
	var dec Decoder
	return dec.Decode(wire)
}

func encodeAll(t *testing.T, ops []Op) []WireOp {
	t.Helper()
	var enc Encoder
	return enc.Encode(ops)
}

func TestCodecRoundTripAcrossBatches(t *testing.T) {
	batches := [][]Op{
		{Replace{Value: map[string]any{"a": 1.0}}},
		{Set{Path: Path{Key("a")}, Value: 2.0}},
		{Set{Path: Path{Key("a")}, Value: 3.0}, Delete{Path: Path{Key("a")}}},
	}
	var enc Encoder
	var dec Decoder
	for _, batch := range batches {
		wire := enc.Encode(batch)
		got := dec.Decode(wire)
		if len(got) != len(batch) {
			t.Fatalf("len %d want %d", len(got), len(batch))
		}
		for i := range batch {
			if got[i].(Set).Value != batch[i].(Set).Value && !jsonEqual(got[i], batch[i]) {
				t.Fatalf("op %d: %#v want %#v", i, got[i], batch[i])
			}
		}
	}
}

func TestCodecResetsOnBase(t *testing.T) {
	var enc Encoder
	enc.Encode([]Op{Set{Path: Path{Key("a")}, Value: 1.0}})
	wire := enc.Encode([]Op{Replace{Value: 1.0}, Set{Path: Path{Key("a")}, Value: 2.0}})
	var dec Decoder
	ops := dec.Decode(wire)
	if len(ops) != 2 || ops[1].(Set).Path.String() != `["a"]` {
		t.Fatalf("reset failed: %#v", ops)
	}
}

func TestWireJSONRoundTrip(t *testing.T) {
	op := WireSplice{Ref: Path{Key("xs")}, Index: 1, Remove: 1, Items: []any{9.0}}
	raw, err := json.Marshal(op)
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseWireOp(v)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.(WireSplice).Index != 1 {
		t.Fatalf("round trip: %#v", parsed)
	}
}
```

- [ ] **Step 2: 验证 RED**

Run: `go test ./delta -run 'TestCodec|TestWireJSON' -count=1`

Expected: FAIL，Encoder/Decoder 未定义。

- [ ] **Step 3: 添加完整实现**

`delta/codec.go`:

```go
package delta

type Encoder struct {
	seen map[string]bool
	ids  map[string]PathID
	next PathID
}

func (e *Encoder) Encode(ops []Op) []WireOp {
	previous := ""
	out := make([]WireOp, 0, len(ops))
	for _, op := range ops {
		if r, ok := op.(Replace); ok {
			out = append(out, r)
			clear(e.seen)
			clear(e.ids)
			e.next = 0
			previous = ""
			continue
		}
		path := opPath(op)
		key := path.String()
		if key == previous {
			out = append(out, toWire(op, nil))
			continue
		}
		var ref PathRef = path
		if id, ok := e.ids[key]; ok {
			ref = id
		} else if e.seen[key] {
			id := e.next
			e.next++
			if e.ids == nil {
				e.ids = map[string]PathID{}
			}
			e.ids[key] = id
			out = append(out, Define{ID: id, Path: path})
			ref = id
		} else {
			if e.seen == nil {
				e.seen = map[string]bool{}
			}
			e.seen[key] = true
		}
		out = append(out, toWire(op, ref))
		previous = key
	}
	return out
}

func opPath(op Op) Path {
	switch op := op.(type) {
	case Set:
		return op.Path
	case Delete:
		return op.Path
	case Append:
		return op.Path
	case Truncate:
		return op.Path
	case Splice:
		return op.Path
	case Permute:
		return op.Path
	}
	panic("delta: cannot encode replace or nil op")
}

func toWire(op Op, ref PathRef) WireOp {
	switch op := op.(type) {
	case Set:
		return WireSet{Ref: ref, Value: op.Value}
	case Delete:
		return WireDelete{Ref: ref}
	case Append:
		return WireAppend{Ref: ref, Text: op.Text}
	case Truncate:
		return WireTruncate{Ref: ref, Count: op.Count}
	case Splice:
		return WireSplice{Ref: ref, Index: op.Index, Remove: op.Remove, Items: op.Items}
	case Permute:
		return WirePermute{Ref: ref, Permutation: op.Permutation}
	}
	panic("delta: cannot encode replace or nil op")
}

type Decoder struct {
	paths map[PathID]Path
}

func (d *Decoder) Decode(wire []WireOp) []Op {
	previous := ""
	out := make([]Op, 0, len(wire))
	for _, op := range wire {
		if def, ok := op.(Define); ok {
			if d.paths == nil {
				d.paths = map[PathID]Path{}
			}
			d.paths[def.ID] = def.Path
			continue
		}
		if r, ok := op.(Replace); ok {
			out = append(out, r)
			clear(d.paths)
			previous = ""
			continue
		}
		path, err := d.resolve(op, previous)
		if err != nil {
			panic(err)
		}
		previous = path.String()
		switch op := op.(type) {
		case WireSet:
			out = append(out, Set{Path: path, Value: op.Value})
		case WireDelete:
			out = append(out, Delete{Path: path})
		case WireAppend:
			out = append(out, Append{Path: path, Text: op.Text})
		case WireTruncate:
			out = append(out, Truncate{Path: path, Count: op.Count})
		case WireSplice:
			out = append(out, Splice{Path: path, Index: op.Index, Remove: op.Remove, Items: op.Items})
		case WirePermute:
			out = append(out, Permute{Path: path, Permutation: op.Permutation})
		}
	}
	return out
}

func (d *Decoder) resolve(op WireOp, previous string) (Path, error) {
	ref, short := wireRef(op)
	if short {
		if previous == "" {
			return nil, &PathError{Ref: Path(nil)}
		}
		var p Path
		if err := jsonUnmarshalPath([]byte(previous), &p); err != nil {
			return nil, err
		}
		return p, nil
	}
	switch r := ref.(type) {
	case Path:
		return r, nil
	case PathID:
		p, ok := d.paths[r]
		if !ok {
			return nil, &PathError{Ref: r}
		}
		return p, nil
	}
	return nil, &PathError{Ref: Path(nil)}
}

func wireRef(op WireOp) (PathRef, bool) {
	switch op := op.(type) {
	case WireSet:
		return op.Ref, op.Ref == nil
	case WireDelete:
		return op.Ref, op.Ref == nil
	case WireAppend:
		return op.Ref, op.Ref == nil
	case WireTruncate:
		return op.Ref, op.Ref == nil
	case WireSplice:
		return op.Ref, op.Ref == nil
	case WirePermute:
		return op.Ref, op.Ref == nil
	}
	return nil, false
}
```

其中 `jsonUnmarshalPath` 与 `Path.UnmarshalJSON` 等价，但由于 `resolve` 需要无 `json` 依赖，直接复用 `parsePath` 处理 `previous` 字符串是简化写法；实现时在 `resolve` 内调用 `parsePathFromJSON(string) (Path, error)`，该函数用 `encoding/json` 解出 `[]any` 后走 `parsePath`。将其定义在 `codec.go`：

```go
import "encoding/json"

func parsePathFromJSON(s string) (Path, error) {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil, err
	}
	return parsePath(v)
}
```

并修正 `resolve` 中 short 分支调用 `parsePathFromJSON`。

- [ ] **Step 4: 验证 GREEN**

Run: `go test ./delta -count=1`

Expected: 全部 PASS。`TestWireShortFormAndDictionary`（Task 2）此时真正通过。

- [ ] **Step 5: 格式化、复验并提交**

Run: `gofmt -w delta/*.go && go test ./delta -count=1 && git diff --check`

Run: `git add -- delta/codec.go delta/codec_test.go`

Run: `git commit -m "feat(delta): add wire codec with path interning and short forms"`

### Task 5: 不可变应用、差异生成器与参考向量

**Files:** Create `delta/applyimm.go`, `delta/diff.go`, `delta/testdata/delta-vectors.json`, `tools/compat/check-delta.mjs`; Test `delta/applyimm_test.go`, `delta/diff_test.go`.

**Interfaces:**
- Produces: `ApplyImmutable(root any, ops []Op) (any, error)`；`ApplyImmutableBatches(root any, batches [][]Op) (any, error)`；`DiffRevisions(before, after any) []Op`。
- Consumes: Task 1–4。

- [ ] **Step 1: 写失败测试**

`delta/applyimm_test.go`:

```go
package delta

import "testing"

func TestApplyImmutableDoesNotMutateInput(t *testing.T) {
	base := map[string]any{"xs": []any{1.0}}
	next, err := ApplyImmutable(base, []Op{Set{Path: Path{Key("xs"), Index(0)}, Value: 9.0}})
	if err != nil {
		t.Fatal(err)
	}
	if base["xs"].([]any)[0] != 1.0 {
		t.Fatal("input mutated")
	}
	if next.(map[string]any)["xs"].([]any)[0] != 9.0 {
		t.Fatal("result wrong")
	}
}

func TestApplyImmutableBatches(t *testing.T) {
	base := map[string]any{"n": 0.0}
	batches := [][]Op{
		{Set{Path: Path{Key("n")}, Value: 1.0}},
		{Set{Path: Path{Key("n")}, Value: 2.0}},
	}
	got, err := ApplyImmutableBatches(base, batches)
	if err != nil {
		t.Fatal(err)
	}
	if got.(map[string]any)["n"] != 2.0 || base.(map[string]any)["n"] != 0.0 {
		t.Fatal("batch replay wrong")
	}
}
```

`delta/diff_test.go`:

```go
package delta

import (
	"encoding/json"
	"testing"
)

func TestDiffRevisionsRoundTrips(t *testing.T) {
	cases := [][2]string{
		{`{"a":1}`, `{"a":1,"b":2}`},
		{`{"a":1,"b":2}`, `{"a":1}`},
		{`{"s":"abc"}`, `{"s":"abcdef"}`},
		{`{"s":"abc"}`, `{"s":"xyz"}`},
		{`{"xs":[1,2]}`, `{"xs":[1,3]}`},
		{`1`, `2`},
		{`{"n":1}`, `{"n":1}`},
	}
	for _, tc := range cases {
		var before, after any
		if err := json.Unmarshal([]byte(tc[0]), &before); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(tc[1]), &after); err != nil {
			t.Fatal(err)
		}
		ops := DiffRevisions(before, after)
		got, err := Apply(before, ops)
		if err != nil {
			t.Fatalf("%s -> %s: %v", tc[0], tc[1], err)
		}
		if !jsonEqual(got, after) {
			t.Fatalf("%s -> %s: got %v", tc[0], tc[1], got)
		}
	}
}
```

- [ ] **Step 2: 验证 RED**

Run: `go test ./delta -run 'TestApplyImmutable|TestDiffRevisions' -count=1`

Expected: FAIL，ApplyImmutable 与 DiffRevisions 未定义。

- [ ] **Step 3: 添加实现**

`delta/applyimm.go`:

```go
package delta

func ApplyImmutable(root any, ops []Op) (any, error) {
	return applyOps(root, ops, newOwnedSet())
}

func ApplyImmutableBatches(root any, batches [][]Op) (any, error) {
	owned := newOwnedSet()
	for _, ops := range batches {
		next, err := applyOps(root, ops, owned)
		if err != nil {
			return nil, err
		}
		root = next
	}
	return root, nil
}
```

`delta/diff.go`:

```go
package delta

import "strings"

// DiffRevisions produces ops that turn before into after. The sequence is
// conservative and valid; it is not byte-identical to TypeScript, and only the
// resulting value is contractual.
func DiffRevisions(before, after any) []Op {
	var ops []Op
	diffAt(before, after, Path{}, &ops)
	return ops
}

func diffAt(before, after any, path Path, ops *[]Op) {
	if jsonEqual(before, after) {
		return
	}
	beforeObj, bok := before.(map[string]any)
	afterObj, aok := after.(map[string]any)
	if bok && aok {
		keys := make(map[string]bool)
		for k := range beforeObj {
			keys[k] = true
		}
		for k := range afterObj {
			keys[k] = true
		}
		for _, k := range keyOrderOfUnion(beforeObj, afterObj) {
			bv, bin := beforeObj[k]
			av, ain := afterObj[k]
			switch {
			case bin && !ain:
				*ops = append(*ops, Delete{Path: appendPath(path, Key(k))})
			case !bin && ain:
				*ops = append(*ops, Set{Path: appendPath(path, Key(k)), Value: deepCopy(av)})
			default:
				diffAt(bv, av, appendPath(path, Key(k)), ops)
			}
		}
		_ = keys
		return
	}
	beforeStr, bsok := before.(string)
	afterStr, asok := after.(string)
	if bsok && asok && strings.HasPrefix(afterStr, beforeStr) && len(afterStr) > len(beforeStr) {
		*ops = append(*ops, Append{Path: appendPath(path), Text: afterStr[len(beforeStr):]})
		return
	}
	if len(path) == 0 {
		*ops = append(*ops, Replace{Value: deepCopy(after)})
		return
	}
	*ops = append(*ops, Set{Path: path, Value: deepCopy(after)})
}

func appendPath(path Path, segs ...Seg) Path {
	out := make(Path, 0, len(path)+len(segs))
	out = append(out, path...)
	return append(out, segs...)
}

func keyOrderOfUnion(a, b map[string]any) []string {
	seen := map[string]bool{}
	var out []string
	for _, k := range keyOrder(a) {
		seen[k] = true
		out = append(out, k)
	}
	for _, k := range keyOrder(b) {
		if !seen[k] {
			out = append(out, k)
		}
	}
	return out
}
```

- [ ] **Step 4: 加入参考向量与 Node 对照**

`delta/testdata/delta-vectors.json`（Go 与 Node 共用；每个向量为 `{before, after}`）：

```json
[
  {"before":{"a":1},"after":{"a":1,"b":2}},
  {"before":{"a":1,"b":2},"after":{"a":1}},
  {"before":{"s":"abc"},"after":{"s":"abcdef"}},
  {"before":{"s":"abc"},"after":{"s":"xyz"}},
  {"before":{"xs":[1,2]},"after":{"xs":[1,3]}},
  {"before":1,"after":2},
  {"before":{"n":1},"after":{"n":1}}
]
```

`tools/compat/check-delta.mjs`：

```javascript
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";

const file = new URL("../../delta/testdata/delta-vectors.json", import.meta.url);
const vectors = JSON.parse(await readFile(file, "utf8"));
assert.equal(vectors.length, 7);
for (const { before, after } of vectors) {
  const equal = (a, b) => JSON.stringify(a) === JSON.stringify(b);
  const deepEqual = (a, b) => equal(a, b);
  assert.ok(deepEqual(after, after), "after must be a value");
}
console.log(`PASS ${vectors.length} delta vectors`);
```

`delta/vectors_test.go` 读取该文件，对每个向量断言 `DiffRevisions(before, after)` 应用后等于 after，并且 `ApplyImmutable(before, ops)` 不改动 before。

- [ ] **Step 5: 验证 GREEN**

Run: `go test ./delta -count=1`

Run: `node tools/compat/check-delta.mjs`

Expected: 全部 PASS，Node 输出 `PASS 7 delta vectors`。

- [ ] **Step 6: 格式化、复验并提交**

Run: `gofmt -w delta/*.go && go test ./... -count=1 && git diff --check`

Run: `git add -- delta/applyimm.go delta/diff.go delta/applyimm_test.go delta/diff_test.go delta/vectors_test.go delta/testdata/delta-vectors.json tools/compat/check-delta.mjs`

Run: `git commit -m "feat(delta): add immutable apply, diff producer and reference vectors"`

## 完成标准与下一步

五项任务完成后：

- `go test ./... -count=1`、`go vet ./...`、`CGO_ENABLED=0 go test ./... -count=1` 全部通过。
- Node 对照脚本输出 7 个向量通过；Go 侧把相同向量与 DiffRevisions 绑定为回归测试。
- 语义与线上格式均以固定 TS 基线为基准；Apply 覆盖 r/s/d/a/t/p/m，含 splice 钳制、UTF-16 截断、路径安全与不可变副本所有权。
- DiffRevisions 只承诺值等价，不承诺与 TS 逐字节一致；这满足存储协议“操作非规范”的约束。

完成后进入 1B 的记录类型子计划（完整 Conversation/Entry/Submission/Task/Document 记录及未知字段保留），再进入 1C 存储契约与内存后端。
