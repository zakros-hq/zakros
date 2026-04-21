// Package audit emits structured audit events to stdout in JSON. Vector on
// each VM picks them up and forwards to Loki on Ariadne per architecture.md
// §12. Every broker-side decision (MCP call, credential touch, termination,
// warning) flows through an Emitter so the forensic surface is uniform.
package audit

import (
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"
)

// Event is one structured audit record. Fields beyond the common set are
// carried in Fields; implementations SHOULD populate Fields with
// broker-specific context (pod_id, broker name, scope, etc.).
type Event struct {
	At       time.Time         `json:"at"`
	Service  string            `json:"service"`
	Category string            `json:"category"`
	Outcome  string            `json:"outcome"`
	Message  string            `json:"message,omitempty"`
	Fields   map[string]string `json:"fields,omitempty"`
}

// Emitter writes audit events somewhere. Phase 1 writes to stdout for
// Vector pickup; tests use a buffer.
type Emitter interface {
	Emit(ev Event)
}

// NewStdoutEmitter returns an Emitter that writes one JSON object per line
// to os.Stdout. Service names the producing service (e.g. "minos", "argus").
func NewStdoutEmitter(service string) Emitter {
	return NewWriterEmitter(service, os.Stdout)
}

// NewWriterEmitter returns an Emitter that writes to w. Useful for tests.
func NewWriterEmitter(service string, w io.Writer) Emitter {
	return &writerEmitter{service: service, w: w}
}

type writerEmitter struct {
	mu      sync.Mutex
	service string
	w       io.Writer
}

func (e *writerEmitter) Emit(ev Event) {
	if ev.At.IsZero() {
		ev.At = time.Now().UTC()
	}
	if ev.Service == "" {
		ev.Service = e.service
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	// Drop audit write failures rather than cascading into caller logic —
	// audit is best-effort from the broker's perspective; the log shipper
	// is responsible for durable retention.
	_ = json.NewEncoder(e.w).Encode(ev)
}
