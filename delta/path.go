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
	return nil, fmt.Errorf("%w: path segment is neither a key nor an index", ErrInvalidOp)
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

func clampInt(n int64) int {
	maxInt := int64(^uint(0) >> 1)
	if n > maxInt {
		return int(maxInt)
	}
	if n < -maxInt-1 {
		return int(-maxInt - 1)
	}
	return int(n)
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
