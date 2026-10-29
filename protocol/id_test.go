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
		raw  string
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
