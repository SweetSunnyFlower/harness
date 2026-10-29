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
		name  string
		found bool
		kind  jsonvalue.Kind
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
