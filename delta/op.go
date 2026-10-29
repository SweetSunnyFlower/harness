package delta

import (
	"encoding/json"
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
	if n, ok := integer(v); ok {
		return PathID(n), nil
	}
	return parsePath(v)
}

func parseCount(v any) (int, error) {
	n, ok := integer(v)
	if !ok || n < 0 {
		return 0, fmt.Errorf("%w: count must be a non-negative integer", ErrInvalidOp)
	}
	return clampInt(n), nil
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
		out[i] = clampInt(n)
	}
	if err := bijection(out); err != nil {
		return nil, err
	}
	return out, nil
}

func bijection(permutation []int) error {
	seen := make([]bool, len(permutation))
	for _, n := range permutation {
		if n < 0 || n >= len(permutation) || seen[n] {
			return fmt.Errorf("%w: permutation is not a bijection", ErrInvalidOp)
		}
		seen[n] = true
	}
	return nil
}

func (Replace) Validate() error { return nil }
func (o Set) Validate() error   { return nonEmpty("s", o.Path) }
func (o Delete) Validate() error {
	return nonEmpty("d", o.Path)
}
func (o Append) Validate() error { return nonEmpty("a", o.Path) }
func (o Truncate) Validate() error {
	if o.Count < 0 {
		return fmt.Errorf("%w: \"t\" count must be non-negative", ErrInvalidOp)
	}
	return nonEmpty("t", o.Path)
}
func (o Splice) Validate() error {
	if o.Index < 0 || o.Remove < 0 {
		return fmt.Errorf("%w: \"p\" index and remove must be non-negative", ErrInvalidOp)
	}
	return o.Path.Validate()
}
func (o Permute) Validate() error {
	if err := bijection(o.Permutation); err != nil {
		return err
	}
	return o.Path.Validate()
}

func (o Define) Validate() error {
	if o.ID < 0 || o.ID > maxSafeInteger {
		return fmt.Errorf("%w: path id out of range", ErrInvalidOp)
	}
	return o.Path.Validate()
}
func (o WireSet) Validate() error { return validateRef(o.Ref) }
func (o WireDelete) Validate() error {
	return validateRef(o.Ref)
}
func (o WireAppend) Validate() error { return validateRef(o.Ref) }
func (o WireTruncate) Validate() error {
	if o.Count < 0 {
		return fmt.Errorf("%w: \"t\" count must be non-negative", ErrInvalidOp)
	}
	return validateRef(o.Ref)
}
func (o WireSplice) Validate() error {
	if o.Index < 0 || o.Remove < 0 {
		return fmt.Errorf("%w: \"p\" index and remove must be non-negative", ErrInvalidOp)
	}
	return validateRef(o.Ref)
}
func (o WirePermute) Validate() error {
	if err := bijection(o.Permutation); err != nil {
		return err
	}
	return validateRef(o.Ref)
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
func (o WireSet) MarshalJSON() ([]byte, error) { return marshalWire("s", o.Ref, o.Value) }
func (o WireDelete) MarshalJSON() ([]byte, error) {
	return marshalWire("d", o.Ref)
}
func (o WireAppend) MarshalJSON() ([]byte, error) { return marshalWire("a", o.Ref, o.Text) }
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
