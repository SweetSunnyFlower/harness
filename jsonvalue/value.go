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
	KindNull   Kind = "null"
	KindBool   Kind = "boolean"
	KindNumber Kind = "number"
	KindString Kind = "string"
	KindArray  Kind = "array"
	KindObject Kind = "object"
)

var (
	ErrJSON  = errors.New("invalid strict JSON")
	ErrKind  = errors.New("unexpected JSON kind")
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
