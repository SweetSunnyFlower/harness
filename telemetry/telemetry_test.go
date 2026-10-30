package telemetry_test

import (
	"testing"
	"time"

	"harness/telemetry"
)

func TestRecorder(t *testing.T) {
	var r telemetry.Recorder
	end := r.Start("op")
	time.Sleep(time.Millisecond)
	end()
	spans := r.Spans()
	if len(spans) != 1 || spans[0].Name != "op" || spans[0].Duration <= 0 {
		t.Fatalf("spans %#v", spans)
	}
}
