package telemetry

import (
	"sync"
	"time"
)

// Span is one completed operation timing.
type Span struct {
	Name     string
	Duration time.Duration
}

// Recorder accumulates spans in completion order.
type Recorder struct {
	mu    sync.Mutex
	spans []Span
}

// Start begins a span and returns a function that ends it.
func (r *Recorder) Start(name string) func() {
	begin := time.Now()
	return func() {
		r.mu.Lock()
		r.spans = append(r.spans, Span{Name: name, Duration: time.Since(begin)})
		r.mu.Unlock()
	}
}

// Spans returns a copy of the recorded spans.
func (r *Recorder) Spans() []Span {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Span, len(r.spans))
	copy(out, r.spans)
	return out
}
