# Phase 1A Wire Protocol Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 交付独立可测的 Go 数据边界：无损 JSON、UTF-16 字符串、类型化安全整数 ID，以及保留未知数据的记录封装。

**Architecture:** JSON 文本作为不可变值持有，已知协议字段按需提取，字符串运算边界使用 UTF-16 code units。protocol 的类型化 ID 依赖 jsonvalue；内部记录 Envelope 依赖二者。此子项目不实现任务调度、delta 或 Storage，后续阶段依据同一基础扩展。

**Tech Stack:** Go 1.25.0+，标准库；Node.js 22.19+ 只运行开发语义对照脚本。当前机器为 Go 1.25.6、Node 22.23.4。

## Global Constraints

- 完整移植 pi-durable 的能力和行为语义，采用 Go 风格 API，运行时不依赖 Node.js。
- 分阶段交付用于控制依赖和验证成本，不缩减最终功能范围。
- 上游提交固定为 `e65b3b88c08412bb8ffbd21fb5dc5756064a6e5a`。
- 跨语言支持声明只覆盖已验证的格式、版本、任务类型、VM 条件和宿主。
- 保留未知数据与不支持版本的失败行为有测试，不做静默损坏或有损降级。
- 本地 module 为 `harness`，`go.mod` 的 go 指令为 `1.25.0`。本子项目不添加第三方依赖。
- 此计划不声称完成总设计第一阶段；其余 1B–1F 必须按详细边界继续交付。

---

## 阅读顺序与执行环境

先读：

1. `/Users/gao/harness-codex/docs/superpowers/specs/2026-10-29-go-durable-harness-design.md`
2. `/Users/gao/harness-codex/docs/superpowers/specs/2026-10-29-phase-1-compatibility-design.md`
3. 固定基线 `packages/durable/src/types.ts`、`packages/chord/src/delta/index.ts`、`packages/durable/src/storage/scan.ts`。

上游参考 checkout 当前位于 `/tmp/pi-durable-reference.0YSJx0/upstream`，它只是检索缓存，不是项目运行依赖。若缓存不存在，在执行时重新取得指定提交并验证 SHA；不能退化为使用最新 main。

本计划内绝对路径以当前仓库为基准。若执行技能创建隔离 worktree，将同一仓库相对路径映射到该 worktree；所有测试和提交命令在所选仓库根目录运行。

任务严格按 1→2→3→4→5 顺序。每个任务都先添加行为测试，观察失败，再加入实现。失败必须来自预期的缺失符号或行为，不接受把工具链或工作目录错误当成测试的 RED。

## 文件布局

| 文件（仓库根目录下） | 职责 |
|---|---|
| `go.mod` | 本地 module 与 Go 最低版本 |
| `jsonvalue/value.go` | 不可变 JSON、种类、数值与 ASCII 协议字段 |
| `jsonvalue/value_test.go` | 语法、隔离、null/缺失和接收者原子更新 |
| `jsonvalue/string.go` | UTF-16 无损提取与构造 |
| `jsonvalue/string_test.go` | 代理项、转义及往返 fuzz |
| `protocol/id.go` | 类型化 ID、Seq、root ID、安全整数边界 |
| `protocol/id_test.go` | 数字形态、拒绝条件、边界和类型区分 |
| `internal/record/envelope.go` | 保留未知内容的内部记录封装 |
| `internal/record/envelope_test.go` | 未知状态与字段往返及拒绝无效 ID |
| `testdata/json-corpus.json` | Go 和 JavaScript 共用语义夹具 |
| `jsonvalue/corpus_test.go` | Go 对夹具预期的验证 |
| `tools/compat/check-json.mjs` | JavaScript 对同一预期的独立验证 |
| `README.md` | 当前阶段的实际使用、限制与验证命令 |

下文代码块给出本子项目的完整文件内容。它们是待执行内容，当前仓库尚未创建这些实现文件。

## Task 1: 不可变 JSON 值与协议字段提取

**Files:**

- Create: `/Users/gao/harness-codex/go.mod`
- Create: `/Users/gao/harness-codex/jsonvalue/value.go`
- Test: `/Users/gao/harness-codex/jsonvalue/value_test.go`

**Interfaces:**

- Consumes: Go 标准库。
- Produces: `Parse([]byte) (Value, error)`；`Value.Kind() Kind`；`Value.Bytes() []byte`；`Value.Number() (float64, error)`；`Value.FieldASCII(string) (Value, bool, error)`；`Value.MarshalJSON() ([]byte, error)`；`(*Value).UnmarshalJSON([]byte) error`。
- `Value` 零值为 null。Kind 枚举和错误哨兵完整定义在下文。FieldASCII 不承担任意 Unicode 键编辑。

- [ ] **Step 1: 建立 module 并写失败测试**

`go.mod`：

```go
module harness

go 1.25.0
```

`jsonvalue/value_test.go`：

```go
package jsonvalue_test

import (
	"encoding/json"
	"errors"
	"testing"

	"harness/jsonvalue"
)

func TestParseRejectsInvalidData(t *testing.T) {
	cases := [][]byte{
		{}, []byte("null true"), []byte("{"), []byte("NaN"),
		[]byte("1e400"), []byte(`{"nested":[1e400]}`),
		{'"', 0xff, '"'},
	}
	for _, input := range cases {
		if _, err := jsonvalue.Parse(input); !errors.Is(err, jsonvalue.ErrJSON) {
			t.Errorf("Parse(%q): got %v, want ErrJSON", input, err)
		}
	}
}

func TestParseOwnsItsBytesAndRetainsRepresentation(t *testing.T) {
	input := []byte(` {"x":"\ud800","big":9007199254740993,"a":[]} `)
	value, err := jsonvalue.Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"x":"\ud800","big":9007199254740993,"a":[]}`
	input[2] = '!'
	output := value.Bytes()
	output[2] = '!'
	if got := string(value.Bytes()); got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestFieldPresenceAndExactNames(t *testing.T) {
	value, err := jsonvalue.Parse([]byte(`{"x":null,"empty":[],"obj":{},"id":1,"\u0069d":2,"ID":3}`))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		found bool
		kind jsonvalue.Kind
	}{
		{"x", true, jsonvalue.KindNull},
		{"missing", false, jsonvalue.KindNull},
		{"empty", true, jsonvalue.KindArray},
		{"obj", true, jsonvalue.KindObject},
	}
	for _, tc := range cases {
		field, found, err := value.FieldASCII(tc.name)
		if err != nil || found != tc.found || field.Kind() != tc.kind {
			t.Fatalf("%s: kind=%s found=%v err=%v", tc.name, field.Kind(), found, err)
		}
	}
	id, _, err := value.FieldASCII("id")
	if err != nil {
		t.Fatal(err)
	}
	number, err := id.Number()
	if err != nil || number != 2 {
		t.Fatalf("last duplicate must win: %v %v", number, err)
	}
	if _, _, err := value.FieldASCII("名字"); !errors.Is(err, jsonvalue.ErrASCII) {
		t.Fatalf("non-ASCII protocol key: %v", err)
	}
	var zero jsonvalue.Value
	if _, _, err := zero.FieldASCII("id"); !errors.Is(err, jsonvalue.ErrKind) {
		t.Fatalf("non-object field lookup: %v", err)
	}
}

func TestZeroAndFailedUnmarshal(t *testing.T) {
	var value jsonvalue.Value
	raw, err := json.Marshal(value)
	if err != nil || string(raw) != "null" {
		t.Fatalf("zero: %s %v", raw, err)
	}
	if err := json.Unmarshal([]byte(`{"keep":1}`), &value); err != nil {
		t.Fatal(err)
	}
	before := string(value.Bytes())
	if err := json.Unmarshal([]byte("1e400"), &value); err == nil {
		t.Fatal("overflow must fail")
	}
	if string(value.Bytes()) != before {
		t.Fatal("failed decode changed receiver")
	}
	if _, err := value.Number(); !errors.Is(err, jsonvalue.ErrKind) {
		t.Fatalf("object as number: %v", err)
	}
}
```

- [ ] **Step 2: 验证测试失败**

Run: `go test ./jsonvalue -count=1`

Expected: FAIL，尚无 jsonvalue 的非测试实现，导入或符号缺失。

- [ ] **Step 3: 添加完整最小实现**

`jsonvalue/value.go`：

```go
package jsonvalue

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"unicode/utf8"
)

type Kind string

const (
	KindNull Kind = "null"
	KindBool Kind = "boolean"
	KindNumber Kind = "number"
	KindString Kind = "string"
	KindArray Kind = "array"
	KindObject Kind = "object"
)

var (
	ErrJSON = errors.New("invalid strict JSON")
	ErrKind = errors.New("unexpected JSON kind")
	ErrASCII = errors.New("protocol field name must be ASCII")
)

// Value owns immutable JSON text. Its zero value is JSON null.
type Value struct {
	raw string
}

func Parse(data []byte) (Value, error) {
	if !utf8.Valid(data) || !json.Valid(data) {
		return Value{}, ErrJSON
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Value{}, fmt.Errorf("%w: %v", ErrJSON, err)
		}
		if number, ok := token.(json.Number); ok {
			value, err := strconv.ParseFloat(string(number), 64)
			if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
				return Value{}, fmt.Errorf("%w: non-finite number", ErrJSON)
			}
		}
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, data); err != nil {
		return Value{}, fmt.Errorf("%w: %v", ErrJSON, err)
	}
	return Value{raw: compact.String()}, nil
}

func (v Value) text() string {
	if v.raw == "" {
		return "null"
	}
	return v.raw
}

func (v Value) Bytes() []byte {
	return []byte(v.text())
}

func (v Value) Kind() Kind {
	switch v.text()[0] {
	case 'n':
		return KindNull
	case 't', 'f':
		return KindBool
	case '"':
		return KindString
	case '[':
		return KindArray
	case '{':
		return KindObject
	default:
		return KindNumber
	}
}

func (v Value) Number() (float64, error) {
	if v.Kind() != KindNumber {
		return 0, ErrKind
	}
	return strconv.ParseFloat(v.text(), 64)
}

// FieldASCII is for fixed protocol keys, not general Unicode object editing.
// Missing and explicit null are distinguished by the found result.
func (v Value) FieldASCII(name string) (Value, bool, error) {
	for _, r := range name {
		if r > 127 {
			return Value{}, false, ErrASCII
		}
	}
	if v.Kind() != KindObject {
		return Value{}, false, ErrKind
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(v.Bytes(), &fields); err != nil {
		return Value{}, false, fmt.Errorf("%w: %v", ErrJSON, err)
	}
	raw, found := fields[name]
	if !found {
		return Value{}, false, nil
	}
	return Value{raw: string(raw)}, true, nil
}

func (v Value) MarshalJSON() ([]byte, error) {
	return v.Bytes(), nil
}

func (v *Value) UnmarshalJSON(data []byte) error {
	next, err := Parse(data)
	if err != nil {
		return err
	}
	*v = next
	return nil
}
```

Parse 中 Decoder.Token 只用于检查数字，不用其解码后的字符串重建数据；否则孤立代理项会丢失。FieldASCII 的临时 map 仅提取已知 ASCII 键，整个值始终保留原始 JSON。

- [ ] **Step 4: 验证行为**

Run: `go test ./jsonvalue -count=1 -v`

Expected: 四个 Test 全部 PASS。

- [ ] **Step 5: 格式化、复验并提交**

Run: `gofmt -w jsonvalue/value.go jsonvalue/value_test.go`

Run: `go test ./jsonvalue -count=1 && git diff --check`

Expected: PASS，无差异空白错误。

Run: `git add -- go.mod jsonvalue/value.go jsonvalue/value_test.go`

Run: `git commit -m "feat: add immutable strict JSON values"`

## Task 2: 保留 UTF-16 代理项的字符串边界

**Files:**

- Create: `/Users/gao/harness-codex/jsonvalue/string.go`
- Test: `/Users/gao/harness-codex/jsonvalue/string_test.go`

**Interfaces:**

- Consumes: `jsonvalue.Parse([]byte) (Value, error)`、`Value.Kind() Kind`、私有 `Value.text()`、`ErrKind`。
- Produces: `Value.StringUnits() ([]uint16, error)`；`StringFromUnits([]uint16) Value`。
- 每次提取返回新切片；构造不保留调用方切片。后续 delta.t 在这些 code units 上切片。

- [ ] **Step 1: 写失败测试**

`jsonvalue/string_test.go`：

```go
package jsonvalue_test

import (
	"errors"
	"slices"
	"testing"

	"harness/jsonvalue"
)

func TestStringUnits(t *testing.T) {
	cases := []struct {
		raw string
		units []uint16
	}{
		{`""`, []uint16{}},
		{`"A"`, []uint16{65}},
		{`"\uD800"`, []uint16{0xd800}},
		{`"\udfff"`, []uint16{0xdfff}},
		{`"\ud83d\ude00"`, []uint16{0xd83d, 0xde00}},
		{`"😀"`, []uint16{0xd83d, 0xde00}},
		{`"中"`, []uint16{0x4e2d}},
		{`"\b\f\n\r\t\/\\\""`, []uint16{8, 12, 10, 13, 9, 47, 92, 34}},
	}
	for _, tc := range cases {
		value, err := jsonvalue.Parse([]byte(tc.raw))
		if err != nil {
			t.Fatal(err)
		}
		got, err := value.StringUnits()
		if err != nil || !slices.Equal(got, tc.units) {
			t.Fatalf("%s: %x %v, want %x", tc.raw, got, err, tc.units)
		}
		again, err := jsonvalue.StringFromUnits(got).StringUnits()
		if err != nil || !slices.Equal(again, tc.units) {
			t.Fatalf("round trip %s: %x %v", tc.raw, again, err)
		}
	}
	var value jsonvalue.Value
	if _, err := value.StringUnits(); !errors.Is(err, jsonvalue.ErrKind) {
		t.Fatalf("null as string: %v", err)
	}
}

func TestStringUnitsDoNotAlias(t *testing.T) {
	input := []uint16{0xd800, 65}
	value := jsonvalue.StringFromUnits(input)
	input[0] = 0
	output, err := value.StringUnits()
	if err != nil {
		t.Fatal(err)
	}
	output[1] = 0
	again, err := value.StringUnits()
	if err != nil || !slices.Equal(again, []uint16{0xd800, 65}) {
		t.Fatalf("aliased units: %x %v", again, err)
	}
}

func FuzzStringUnitsRoundTrip(f *testing.F) {
	f.Add([]byte{0x00, 0xd8, 0x00, 0xdc})
	f.Add([]byte{0xff})
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		units := make([]uint16, (len(data)+1)/2)
		for i, b := range data {
			units[i/2] |= uint16(b) << (8 * (i % 2))
		}
		value := jsonvalue.StringFromUnits(units)
		parsed, err := jsonvalue.Parse(value.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		got, err := parsed.StringUnits()
		if err != nil || !slices.Equal(got, units) {
			t.Fatalf("got %x, want %x: %v", got, units, err)
		}
	})
}
```

- [ ] **Step 2: 验证测试失败**

Run: `go test ./jsonvalue -run TestStringUnits -count=1`

Expected: FAIL，StringUnits/StringFromUnits 未定义。

- [ ] **Step 3: 添加完整实现**

`jsonvalue/string.go`：

```go
package jsonvalue

import (
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

func (v Value) StringUnits() ([]uint16, error) {
	if v.Kind() != KindString {
		return nil, ErrKind
	}
	raw := v.text()
	units := make([]uint16, 0, len(raw)-2)
	for i := 1; i < len(raw)-1; {
		if raw[i] == '\\' {
			i++
			escape := raw[i]
			i++
			switch escape {
			case '"', '\\', '/':
				units = append(units, uint16(escape))
			case 'b':
				units = append(units, 8)
			case 'f':
				units = append(units, 12)
			case 'n':
				units = append(units, 10)
			case 'r':
				units = append(units, 13)
			case 't':
				units = append(units, 9)
			case 'u':
				number, err := strconv.ParseUint(raw[i:i+4], 16, 16)
				if err != nil {
					return nil, ErrJSON
				}
				units = append(units, uint16(number))
				i += 4
			default:
				return nil, ErrJSON
			}
			continue
		}
		r, width := utf8.DecodeRuneInString(raw[i : len(raw)-1])
		i += width
		if r <= 0xffff {
			units = append(units, uint16(r))
		} else {
			high, low := utf16.EncodeRune(r)
			units = append(units, uint16(high), uint16(low))
		}
	}
	return units, nil
}

func StringFromUnits(units []uint16) Value {
	const hex = "0123456789abcdef"
	var out strings.Builder
	out.WriteByte('"')
	for _, unit := range units {
		out.WriteString(`\u`)
		out.WriteByte(hex[(unit>>12)&15])
		out.WriteByte(hex[(unit>>8)&15])
		out.WriteByte(hex[(unit>>4)&15])
		out.WriteByte(hex[unit&15])
	}
	out.WriteByte('"')
	return Value{raw: out.String()}
}
```

- [ ] **Step 4: 运行边界测试与短 fuzz**

Run: `go test ./jsonvalue -count=1`

Expected: 全部 PASS。

Run: `go test ./jsonvalue -run '^$' -fuzz '^FuzzStringUnitsRoundTrip$' -fuzztime=5s -parallel=2`

Expected: PASS，不出现代理项被替换、越界或输入输出别名问题。

- [ ] **Step 5: 格式化、复验并提交**

Run: `gofmt -w jsonvalue/string.go jsonvalue/string_test.go`

Run: `go test ./jsonvalue -count=1 && git diff --check`

Run: `git add -- jsonvalue/string.go jsonvalue/string_test.go`

Run: `git commit -m "feat: preserve JavaScript UTF-16 string values"`

## Task 3: 类型化 ID、安全整数与 Seq

**Files:**

- Create: `/Users/gao/harness-codex/protocol/id.go`
- Test: `/Users/gao/harness-codex/protocol/id_test.go`

**Interfaces:**

- Consumes: `jsonvalue.Parse([]byte) (Value, error)`；`Value.Number() (float64, error)`。
- Produces: 泛型 `ID[K]`；别名 `RecordID`、`ConversationID`、`EntryID`、`TaskID`、`SubmissionID`、`DocumentID`、`Seq`；`MaxSafeInteger`、`RootConversationID`；`ID.Valid() bool` 及 JSON 编解码。
- ID 类型名区分实体种类，显式 Go 类型转换仍可进行；持久化边界再次校验数值。

- [ ] **Step 1: 写失败测试**

`protocol/id_test.go`：

```go
package protocol_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"harness/protocol"
)

func TestIDValidForms(t *testing.T) {
	cases := []struct {
		raw string
		want protocol.RecordID
	}{
		{"1", 1}, {"1.0", 1}, {"1e0", 1},
		{"9007199254740991", protocol.RecordID(protocol.MaxSafeInteger)},
	}
	for _, tc := range cases {
		var id protocol.RecordID
		if err := json.Unmarshal([]byte(tc.raw), &id); err != nil || id != tc.want {
			t.Fatalf("%s: %v %v", tc.raw, id, err)
		}
		raw, err := json.Marshal(id)
		if err != nil {
			t.Fatal(err)
		}
		var again protocol.RecordID
		if err := json.Unmarshal(raw, &again); err != nil || again != id {
			t.Fatalf("round trip: %v %v", again, err)
		}
	}
}

func TestIDRejectsInvalidFormsWithoutMutation(t *testing.T) {
	for _, raw := range []string{
		"0", "-0", "-1", "1.5", "9007199254740992",
		"9007199254740993", "1e400", `"1"`, "null", "true", "[]", "{}",
	} {
		id := protocol.RecordID(7)
		err := json.Unmarshal([]byte(raw), &id)
		if !errors.Is(err, protocol.ErrID) || id != 7 {
			t.Fatalf("%s: id=%v err=%v", raw, id, err)
		}
	}
	for _, id := range []protocol.RecordID{0, protocol.RecordID(protocol.MaxSafeInteger + 1)} {
		if _, err := json.Marshal(id); !errors.Is(err, protocol.ErrID) {
			t.Fatalf("encoded invalid ID %v: %v", id, err)
		}
	}
}

func TestIDKindsAndRoot(t *testing.T) {
	if protocol.RootConversationID != 1 || !protocol.RootConversationID.Valid() {
		t.Fatal("root conversation must be 1")
	}
	types := []reflect.Type{
		reflect.TypeOf(protocol.RecordID(1)),
		reflect.TypeOf(protocol.ConversationID(1)),
		reflect.TypeOf(protocol.EntryID(1)),
		reflect.TypeOf(protocol.TaskID(1)),
		reflect.TypeOf(protocol.SubmissionID(1)),
		reflect.TypeOf(protocol.DocumentID(1)),
		reflect.TypeOf(protocol.Seq(1)),
	}
	seen := make(map[reflect.Type]bool)
	for _, typ := range types {
		if seen[typ] {
			t.Fatalf("record kinds share type %v", typ)
		}
		seen[typ] = true
	}
}
```

- [ ] **Step 2: 验证测试失败**

Run: `go test ./protocol -count=1`

Expected: FAIL，protocol 实现尚不存在。

- [ ] **Step 3: 添加完整实现**

`protocol/id.go`：

```go
package protocol

import (
	"errors"
	"fmt"
	"math"
	"strconv"

	"harness/jsonvalue"
)

const MaxSafeInteger uint64 = 1<<53 - 1

var ErrID = errors.New("ID must be a positive JavaScript safe integer")

type ID[K any] uint64

type recordKind struct{}
type conversationKind struct{}
type entryKind struct{}
type taskKind struct{}
type submissionKind struct{}
type documentKind struct{}
type sequenceKind struct{}

type RecordID = ID[recordKind]
type ConversationID = ID[conversationKind]
type EntryID = ID[entryKind]
type TaskID = ID[taskKind]
type SubmissionID = ID[submissionKind]
type DocumentID = ID[documentKind]
type Seq = ID[sequenceKind]

const RootConversationID ConversationID = 1

func (id ID[K]) Valid() bool {
	return uint64(id) >= 1 && uint64(id) <= MaxSafeInteger
}

func (id ID[K]) MarshalJSON() ([]byte, error) {
	if !id.Valid() {
		return nil, ErrID
	}
	return strconv.AppendUint(nil, uint64(id), 10), nil
}

func (id *ID[K]) UnmarshalJSON(data []byte) error {
	value, err := jsonvalue.Parse(data)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrID, err)
	}
	number, err := value.Number()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrID, err)
	}
	if number < 1 || number > float64(MaxSafeInteger) || math.Trunc(number) != number {
		return ErrID
	}
	*id = ID[K](uint64(number))
	return nil
}
```

这里限制的是解析后的 JavaScript number 的安全整数语义。通用 JSON Value 不会因普通数据数字超出安全整数范围而拒绝它；只有 ID/Seq 使用此限制。

- [ ] **Step 4: 验证 ID 与上游边界**

Run: `go test ./protocol ./jsonvalue -count=1 -v`

Expected: PASS；最大安全整数可往返，下一整数拒绝。

- [ ] **Step 5: 格式化、复验并提交**

Run: `gofmt -w protocol/id.go protocol/id_test.go`

Run: `go test ./protocol ./jsonvalue -count=1 && git diff --check`

Run: `git add -- protocol/id.go protocol/id_test.go`

Run: `git commit -m "feat: define typed durable IDs and sequences"`

## Task 4: 未知字段与状态的无损记录封装

**Files:**

- Create: `/Users/gao/harness-codex/internal/record/envelope.go`
- Test: `/Users/gao/harness-codex/internal/record/envelope_test.go`

**Interfaces:**

- Consumes: `jsonvalue.Parse`、`KindObject`、`Value.FieldASCII`、`Value.MarshalJSON`、`protocol.RecordID` 的 JSON 编解码。
- Produces: `record.Parse([]byte) (Envelope, error)`；`Envelope.ID() protocol.RecordID`；`Envelope.Value() jsonvalue.Value`；`Envelope.FieldASCII(string) (jsonvalue.Value, bool, error)`；JSON 编解码。
- 只验证对象和 ID；后续完整记录解码负责具体 kind/version/state 的语义。

- [ ] **Step 1: 写失败测试**

`internal/record/envelope_test.go`：

```go
package record_test

import (
	"encoding/json"
	"errors"
	"testing"

	"harness/internal/record"
	"harness/jsonvalue"
)

func TestEnvelopeRetainsUnknownState(t *testing.T) {
	raw := `{"id":5,"kind":"app.unknown","version":99,"state":{"status":"sleeping","checkpoint":{"phase":"run","text":"\ud800"},"until":1000},"future":{"x":null},"empty":[]}`
	value, err := record.Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if value.ID() != 5 {
		t.Fatalf("ID %v", value.ID())
	}
	encoded, err := json.Marshal(value)
	if err != nil || string(encoded) != raw {
		t.Fatalf("unknown fields lost: %s %v", encoded, err)
	}
	future, found, err := value.FieldASCII("future")
	if err != nil || !found || future.Kind() != jsonvalue.KindObject {
		t.Fatalf("future field: %v %v %v", future.Kind(), found, err)
	}
	output := value.Value().Bytes()
	output[0] = '!'
	if string(value.Value().Bytes()) != raw {
		t.Fatal("envelope exposes mutable bytes")
	}
}

func TestEnvelopeRejectsInvalidIdentity(t *testing.T) {
	for _, raw := range []string{
		"null", "[]", "{}", `{"ID":1}`, `{"id":null}`,
		`{"id":0}`, `{"id":"1"}`, `{"id":9007199254740992}`,
	} {
		if _, err := record.Parse([]byte(raw)); !errors.Is(err, record.ErrRecord) {
			t.Fatalf("%s: %v", raw, err)
		}
	}
	var zero record.Envelope
	if _, err := json.Marshal(zero); !errors.Is(err, record.ErrRecord) {
		t.Fatalf("encoded zero envelope: %v", err)
	}
}

func TestEnvelopeFailedUnmarshalPreservesRecord(t *testing.T) {
	value, err := record.Parse([]byte(`{"id":8,"extra":null}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"id":0}`), &value); err == nil {
		t.Fatal("accepted invalid ID")
	}
	if value.ID() != 8 || string(value.Value().Bytes()) != `{"id":8,"extra":null}` {
		t.Fatal("failed decode changed envelope")
	}
}
```

- [ ] **Step 2: 验证测试失败**

Run: `go test ./internal/record -count=1`

Expected: FAIL，record 的非测试实现尚不存在。

- [ ] **Step 3: 添加完整实现**

`internal/record/envelope.go`：

```go
package record

import (
	"encoding/json"
	"errors"
	"fmt"

	"harness/jsonvalue"
	"harness/protocol"
)

var ErrRecord = errors.New("record must be an object with a valid ID")

// Envelope preserves unknown data. It does not validate task or entry semantics.
type Envelope struct {
	id protocol.RecordID
	value jsonvalue.Value
}

func Parse(data []byte) (Envelope, error) {
	value, err := jsonvalue.Parse(data)
	if err != nil {
		return Envelope{}, fmt.Errorf("%w: %v", ErrRecord, err)
	}
	if value.Kind() != jsonvalue.KindObject {
		return Envelope{}, ErrRecord
	}
	rawID, found, err := value.FieldASCII("id")
	if err != nil || !found {
		return Envelope{}, ErrRecord
	}
	var id protocol.RecordID
	if err := json.Unmarshal(rawID.Bytes(), &id); err != nil {
		return Envelope{}, fmt.Errorf("%w: %v", ErrRecord, err)
	}
	return Envelope{id: id, value: value}, nil
}

func (e Envelope) ID() protocol.RecordID {
	return e.id
}

func (e Envelope) Value() jsonvalue.Value {
	return e.value
}

func (e Envelope) FieldASCII(name string) (jsonvalue.Value, bool, error) {
	return e.value.FieldASCII(name)
}

func (e Envelope) MarshalJSON() ([]byte, error) {
	if !e.id.Valid() {
		return nil, ErrRecord
	}
	return e.value.MarshalJSON()
}

func (e *Envelope) UnmarshalJSON(data []byte) error {
	next, err := Parse(data)
	if err != nil {
		return err
	}
	*e = next
	return nil
}
```

- [ ] **Step 4: 验证未知内容保留与拒绝条件**

Run: `go test ./internal/record ./protocol ./jsonvalue -count=1 -v`

Expected: PASS。未知任务 kind/version 可被保留，不会在此数据层被误执行或丢弃。

- [ ] **Step 5: 格式化、复验并提交**

Run: `gofmt -w internal/record/envelope.go internal/record/envelope_test.go`

Run: `go test ./... -count=1 && git diff --check`

Run: `git add -- internal/record/envelope.go internal/record/envelope_test.go`

Run: `git commit -m "feat: preserve unknown durable record content"`

## Task 5: 独立 JavaScript 对照与当前能力说明

**Files:**

- Create: `/Users/gao/harness-codex/testdata/json-corpus.json`
- Test: `/Users/gao/harness-codex/jsonvalue/corpus_test.go`
- Create: `/Users/gao/harness-codex/tools/compat/check-json.mjs`
- Create: `/Users/gao/harness-codex/README.md`

**Interfaces:**

- Consumes: `jsonvalue.Parse`、`Value.Kind`、`Value.Number`、`Value.StringUnits`。
- Produces: 固定夹具 JSON；Go `TestCorpus` 与 Node 对照脚本；两者都独立核对显式预期，不互相生成预期。
- 此测试只验证 JavaScript 数据语义；上游真实 Storage 往返属于 1D–1F。

- [ ] **Step 1: 先写读取尚不存在夹具的测试**

`jsonvalue/corpus_test.go`：

```go
package jsonvalue_test

import (
	"encoding/json"
	"math"
	"os"
	"slices"
	"testing"

	"harness/jsonvalue"
)

func TestCorpus(t *testing.T) {
	raw, err := os.ReadFile("../testdata/json-corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name string `json:"name"`
		Raw string `json:"raw"`
		Valid bool `json:"valid"`
		Kind jsonvalue.Kind `json:"kind"`
		Units *[]uint16 `json:"units"`
		Number *float64 `json:"number"`
		NegativeZero bool `json:"negativeZero"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 13 {
		t.Fatalf("want 13 reference cases, got %d", len(cases))
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			value, err := jsonvalue.Parse([]byte(tc.Raw))
			if (err == nil) != tc.Valid {
				t.Fatalf("valid=%v err=%v", tc.Valid, err)
			}
			if !tc.Valid {
				return
			}
			if value.Kind() != tc.Kind {
				t.Fatalf("kind=%s, want %s", value.Kind(), tc.Kind)
			}
			if tc.Units != nil {
				got, err := value.StringUnits()
				if err != nil || !slices.Equal(got, *tc.Units) {
					t.Fatalf("units=%x want=%x err=%v", got, *tc.Units, err)
				}
			}
			if tc.Number != nil {
				got, err := value.Number()
				if err != nil || got != *tc.Number {
					t.Fatalf("number=%v want=%v err=%v", got, *tc.Number, err)
				}
				if got == 0 && math.Signbit(got) != tc.NegativeZero {
					t.Fatalf("negative zero=%v, want %v", math.Signbit(got), tc.NegativeZero)
				}
			}
		})
	}
}
```

- [ ] **Step 2: 验证参考测试失败**

Run: `go test ./jsonvalue -run '^TestCorpus$' -count=1`

Expected: FAIL，`../testdata/json-corpus.json` 不存在。

- [ ] **Step 3: 加入显式夹具与独立参考实现**

`testdata/json-corpus.json`：

```json
[
  {"name":"null","raw":"null","valid":true,"kind":"null"},
  {"name":"array","raw":"[]","valid":true,"kind":"array"},
  {"name":"object","raw":"{}","valid":true,"kind":"object"},
  {"name":"bool","raw":"true","valid":true,"kind":"boolean"},
  {"name":"empty-string","raw":"\"\"","valid":true,"kind":"string","units":[]},
  {"name":"high-surrogate","raw":"\"\\ud800\"","valid":true,"kind":"string","units":[55296]},
  {"name":"low-surrogate","raw":"\"\\udfff\"","valid":true,"kind":"string","units":[57343]},
  {"name":"astral","raw":"\"😀\"","valid":true,"kind":"string","units":[55357,56832]},
  {"name":"negative-zero","raw":"-0","valid":true,"kind":"number","number":0,"negativeZero":true},
  {"name":"rounded-number","raw":"9007199254740993","valid":true,"kind":"number","number":9007199254740992},
  {"name":"overflow","raw":"1e400","valid":false},
  {"name":"nested-overflow","raw":"{\"x\":[1e400]}","valid":false},
  {"name":"trailing-value","raw":"null true","valid":false}
]
```

`tools/compat/check-json.mjs`：

```javascript
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";

const file = new URL("../../testdata/json-corpus.json", import.meta.url);
const cases = JSON.parse(await readFile(file, "utf8"));
assert.equal(cases.length, 13);

function finite(value) {
  if (typeof value === "number") assert.ok(Number.isFinite(value));
  if (value !== null && typeof value === "object") {
    for (const child of Object.values(value)) finite(child);
  }
}

for (const test of cases) {
  let value;
  let valid = true;
  try {
    value = JSON.parse(test.raw);
    finite(value);
  } catch {
    valid = false;
  }
  assert.equal(valid, test.valid, test.name);
  if (!valid) continue;
  const kind = value === null ? "null" : Array.isArray(value) ? "array" : typeof value;
  assert.equal(kind, test.kind, test.name);
  if (Object.hasOwn(test, "units")) {
    const units = Array.from({ length: value.length }, (_, i) => value.charCodeAt(i));
    assert.deepEqual(units, test.units, test.name);
  }
  if (Object.hasOwn(test, "number")) {
    assert.ok(value === test.number, test.name);
    if (value === 0) assert.equal(Object.is(value, -0), test.negativeZero ?? false, test.name);
  }
}
console.log(`PASS ${cases.length} JavaScript value cases`);
```

- [ ] **Step 4: 分别运行 Go 与 JavaScript 对照**

Run: `go test ./jsonvalue -run '^TestCorpus$' -count=1 -v`

Expected: 13 个子测试 PASS。

Run: `node tools/compat/check-json.mjs`

Expected: `PASS 13 JavaScript value cases`。

- [ ] **Step 5: 写清楚当前能力，不宣称存储或 Agent 已实现**

`README.md`：

````markdown
# Go Durable Harness

面向 pi-durable 完整能力移植的 Go 包，运行时不依赖 Node.js。
上游参考提交为 e65b3b88c08412bb8ffbd21fb5dc5756064a6e5a。

当前交付为阶段 1A 数据边界：

- jsonvalue：不可变 JSON、缺失字段与 null 的区分、UTF-16 字符串往返。
- protocol：类型化 ID/Seq 与 JavaScript 安全整数限制。
- internal/record：保留未知字段和状态的内部记录封装。

此阶段尚未实现 Storage、持久化任务或 Agent 执行；不能用于恢复
pi-durable 会话。完整交付顺序见 docs/superpowers/specs。

## 使用

本地 module 名为 harness，要求 Go 1.25.0 或更新版本。

```go
package main

import (
	"fmt"

	"harness/jsonvalue"
)

func main() {
	value, err := jsonvalue.Parse([]byte(`{"text":"\ud800"}`))
	if err != nil {
		panic(err)
	}
	text, found, err := value.FieldASCII("text")
	if err != nil || !found {
		panic("text missing")
	}
	units, err := text.StringUnits()
	if err != nil {
		panic(err)
	}
	fmt.Printf("%04x\n", units)
}
```

FieldASCII 只用于已知 ASCII 协议键。Envelope 只检查对象和 ID，
不代表任务记录的所有状态字段已经有效。

## 验证

```sh
go test ./... -count=1
go vet ./...
CGO_ENABLED=0 go test ./... -count=1
node tools/compat/check-json.mjs
```

最后一条命令是开发期 JavaScript 语义对照，需要 Node.js 22.19+。
Go 包的运行和 Go 单元测试均不调用 Node.js。
数据语义夹具通过不等于 SQLite/JSONL 已完成跨语言互操作。
````

- [ ] **Step 6: 完整验证当前子项目**

Run: `gofmt -w jsonvalue/corpus_test.go`

Run: `go test ./... -count=1`

Run: `go vet ./...`

Run: `CGO_ENABLED=0 go test ./... -count=1`

Run: `node tools/compat/check-json.mjs`

Run: `git diff --check`

Expected: 全部退出码为 0，Go 三个包的测试均 PASS。CGO_ENABLED=0 只验证此子项目，不扩展为后续 SQLite 驱动已验证的声明。

- [ ] **Step 7: 提交夹具、验证器与使用说明**

Run: `git add -- testdata/json-corpus.json jsonvalue/corpus_test.go tools/compat/check-json.mjs README.md`

Run: `git commit -m "test: cross-check wire values against JavaScript"`

## 子项目出口与下一步

五个任务完成后，检查：

- 所有五个任务的实现、测试和提交均有结果。
- Value 及 Envelope 不暴露可变底层数据。
- 孤立代理项与安全整数边界已验证；不会把 null 当作字段缺失。
- JavaScript 参考只作为开发依赖。
- 无 Storage、delta 或调度器的占位实现混入交付。

然后编写 1B“完整记录与增量协议”的可执行计划，消费本计划定义的 Value、UTF-16 和 ID。1B 必须实现任意用户对象键及全部 delta 操作，不能把 FieldASCII 推广为 Unicode 对象编辑器。继续推进 1C–1F 才能完成整个兼容基础阶段。

## 计划自审记录

- 总设计中的完整移植、无 Node 运行依赖、未知字段保留和兼容声明边界均已映射到本子项目；存储及运行时要求显式映射到后续子项目，没有删除。
- Task 1 定义 Value/Kind/错误与字段访问；Task 2 仅扩展字符串；Task 3 只依赖 Value；Task 4 消费 Value/RecordID；Task 5 消费上述已定义接口。
- 每个代码步骤包含完整文件内容，测试包含具体断言及预期失败。代码内容尚未执行，计划检查不等于 Go 测试已通过。
- 1B–1F 尚未形成可执行计划，本文件不宣称整个第一阶段已经准备好一次性实现。

2026-10-29 文档检查结果：5 个任务、27 个勾选步骤、5 组接口定义；10 个 Go 源码片段通过 gofmt 语法解析，JavaScript 脚本通过语法检查，13 个夹具预期通过独立 JavaScript 求值核对。Markdown 围栏与表格结构通过检查。尚未创建或运行 Go 实现，尚未运行上述 Go 测试。
