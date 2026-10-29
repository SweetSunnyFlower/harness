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
