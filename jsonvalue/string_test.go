package jsonvalue_test

import (
	"errors"
	"slices"
	"testing"

	"harness/jsonvalue"
)

func TestStringUnits(t *testing.T) {
	cases := []struct {
		raw   string
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
