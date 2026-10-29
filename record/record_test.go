package record

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func decodeAny(t *testing.T, raw string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func assertJSONEqual(t *testing.T, a, b string) {
	t.Helper()
	if !reflect.DeepEqual(decodeAny(t, a), decodeAny(t, b)) {
		t.Fatalf("JSON differs:\n%s\n%s", a, b)
	}
}

func roundTrip[T any](t *testing.T, raw string, into *T) []byte {
	t.Helper()
	if err := json.Unmarshal([]byte(raw), into); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(into)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestConversationRoundTrip(t *testing.T) {
	raw := `{"id":1,"parent":{"conversationId":2,"at":10},"owner":{"conversationId":3,"taskId":4},"future":true}`
	var rec ConversationRecord
	out := roundTrip(t, raw, &rec)
	assertJSONEqual(t, raw, string(out))
	if !reflect.DeepEqual(rec.extra, map[string]json.RawMessage{"future": json.RawMessage("true")}) {
		t.Fatalf("extra %v", rec.extra)
	}
	if err := rec.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestEntryRoundTrip(t *testing.T) {
	raw := `{"id":5,"conversationId":1,"kind":"app.note","model":[{"role":"user"}],"data":{"x":null},"head":6,"edits":[],"byTaskId":7,"future":"y"}`
	var rec EntryRecord
	out := roundTrip(t, raw, &rec)
	assertJSONEqual(t, raw, string(out))
	if err := rec.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSubmissionVariants(t *testing.T) {
	valid := []string{
		`{"id":1,"conversationId":1,"type":"input","status":"queued"}`,
		`{"id":2,"conversationId":1,"type":"input","status":"placed","entry":10}`,
		`{"id":3,"conversationId":1,"type":"input","status":"done","entry":10,"answer":11}`,
		`{"id":4,"conversationId":1,"type":"input","status":"unanswered","reason":"run_failed","detail":{"k":1}}`,
		`{"id":5,"conversationId":1,"type":"write","status":"queued"}`,
		`{"id":6,"conversationId":1,"type":"continue","status":"done","answer":11}`,
	}
	for _, raw := range valid {
		var rec SubmissionRecord
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if err := rec.Validate(); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		out, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		assertJSONEqual(t, raw, string(out))
	}
	invalid := []string{
		`{"id":1,"conversationId":1,"type":"input","status":"done","entry":10}`,
		`{"id":1,"conversationId":1,"type":"input","status":"bogus"}`,
		`{"id":1,"conversationId":1,"type":"other","status":"queued"}`,
	}
	for _, raw := range invalid {
		var rec SubmissionRecord
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			t.Fatal(err)
		}
		if !errors.Is(rec.Validate(), ErrRecord) {
			t.Fatalf("%s: expected validation error", raw)
		}
	}
}

func TestTaskRoundTripAndEmptyArray(t *testing.T) {
	raw := `{"id":1,"conversationId":1,"kind":"app.task","version":2,"input":{"x":1},"background":false,"abortRequested":false,"state":{"status":"waiting","checkpoint":{"phase":"run"},"on":[],"policy":"allSettled"},"future":3}`
	var rec TaskRecord
	out := roundTrip(t, raw, &rec)
	assertJSONEqual(t, raw, string(out))
	if err := rec.Validate(); err != nil {
		t.Fatal(err)
	}
	terminal := `{"id":2,"conversationId":1,"kind":"app.task","version":1,"input":null,"background":true,"abortRequested":false,"state":{"status":"terminal","outcome":{"status":"completed","result":{"ok":true}}}}`
	var done TaskRecord
	if err := json.Unmarshal([]byte(terminal), &done); err != nil {
		t.Fatal(err)
	}
	if err := done.Validate(); err != nil {
		t.Fatal(err)
	}
	var bad TaskRecord
	if err := json.Unmarshal([]byte(`{"id":1,"conversationId":1,"kind":"x","version":0,"input":null,"background":false,"abortRequested":false,"state":{"status":"pending"}}`), &bad); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(bad.Validate(), ErrRecord) {
		t.Fatal("version 0 must fail")
	}
}

func TestDocumentRoundTripAndValidation(t *testing.T) {
	raw := `{"id":8,"kind":"app.doc","createdAt":3,"scope":{"kind":"conversation","conversationId":2},"history":"rewindable","fork":"asOf","future":[1]}`
	var rec DocumentRecord
	out := roundTrip(t, raw, &rec)
	assertJSONEqual(t, raw, string(out))
	if err := rec.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := []string{
		`{"id":8,"kind":"app.doc","createdAt":3,"scope":{"kind":"conversation"},"history":"latest","fork":"asOf"}`,
		`{"id":8,"kind":"app.doc","createdAt":3,"scope":{"kind":"session","conversationId":1}}`,
		`{"id":8,"kind":"app.doc","createdAt":3,"scope":{"kind":"bogus"}}`,
	}
	for _, raw := range bad {
		var rec DocumentRecord
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			t.Fatal(err)
		}
		if !errors.Is(rec.Validate(), ErrRecord) {
			t.Fatalf("%s: expected validation error", raw)
		}
	}
}

func TestRecordRejectsInvalidID(t *testing.T) {
	var rec ConversationRecord
	if err := json.Unmarshal([]byte(`{"id":0}`), &rec); !errors.Is(err, ErrRecord) {
		t.Fatalf("invalid id: %v", err)
	}
}
