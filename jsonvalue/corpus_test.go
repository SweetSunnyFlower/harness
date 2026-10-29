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
		Name         string         `json:"name"`
		Raw          string         `json:"raw"`
		Valid        bool           `json:"valid"`
		Kind         jsonvalue.Kind `json:"kind"`
		Units        *[]uint16      `json:"units"`
		Number       *float64       `json:"number"`
		NegativeZero bool           `json:"negativeZero"`
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
