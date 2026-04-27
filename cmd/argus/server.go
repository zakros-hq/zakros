package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/zakros-hq/zakros/minos/argus"
	"github.com/zakros-hq/zakros/pkg/audit"
	"github.com/zakros-hq/zakros/pkg/brokerauth"
)

// server bundles the Argus daemon's HTTP runtime dependencies. One per
// process; routes() declares the handler tree.
type server struct {
	logger   *slog.Logger
	audit    audit.Emitter
	argus    *argus.Argus
	verifier *brokerauth.Verifier
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	// Pod heartbeat — JWT carries audience=argus, scope=heartbeat.
	// Subject is "task:<task_id>"; the body's task_id must match.
	mux.Handle("POST /argus/heartbeat", s.verifier.Require("heartbeat", http.HandlerFunc(s.handleHeartbeat)))
	// Broker push events — JWT carries audience=argus, scope=event.
	// Slice J accepts and audit-logs; Phase 2 K's drift-detection
	// rules engine consumes these.
	mux.Handle("POST /argus/events", s.verifier.Require("event", http.HandlerFunc(s.handleEvent)))
	return mux
}

func (s *server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// heartbeatRequest is the body shape pods/sidecars POST.
type heartbeatRequest struct {
	TaskID string `json:"task_id"`
}

// handleHeartbeat extracts the task id from the body, cross-checks
// against the JWT subject (must be "task:<task_id>" and match), and
// forwards to the Argus rules engine.
func (s *server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	claims := brokerauth.ClaimsFromContext(r.Context())
	var body heartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("decode body: %v", err))
		return
	}
	if body.TaskID == "" {
		writeError(w, http.StatusBadRequest, "task_id required")
		return
	}
	id, err := uuid.Parse(body.TaskID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid task_id")
		return
	}

	// Subject must encode the same task — prevents a compromised pod
	// from heartbeating on behalf of another.
	const prefix = "task:"
	if claims == nil || len(claims.Subject) <= len(prefix) || claims.Subject[:len(prefix)] != prefix {
		writeError(w, http.StatusForbidden, "subject not task-scoped")
		return
	}
	if claims.Subject[len(prefix):] != body.TaskID {
		writeError(w, http.StatusForbidden, "task id mismatch")
		return
	}

	s.argus.Heartbeat(id)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// eventRequest is the body shape brokers POST. Generic by design —
// Slice J just records the event in audit; Phase 2 K wires per-type
// rules-engine handling (read-untrusted-then-high-blast sequences,
// sustained denial counters, etc.).
type eventRequest struct {
	// Type categorizes the event: "scope-deny", "audit", "drift" etc.
	Type string `json:"type"`
	// Pod identifies the originating pod when applicable. Optional —
	// some broker-side events (config-load failures, key rotation
	// signals) aren't pod-scoped.
	Pod string `json:"pod,omitempty"`
	// Fields is the free-form structured payload the audit emit
	// preserves. Per-broker schemas land in their own packages once
	// real consumers exist.
	Fields map[string]string `json:"fields,omitempty"`
	// Message is the short summary the audit emitter records.
	Message string `json:"message,omitempty"`
}

func (s *server) handleEvent(w http.ResponseWriter, r *http.Request) {
	claims := brokerauth.ClaimsFromContext(r.Context())
	var body eventRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("decode body: %v", err))
		return
	}
	if body.Type == "" {
		writeError(w, http.StatusBadRequest, "type required")
		return
	}
	fields := map[string]string{
		"type": body.Type,
		"sub":  claims.Subject,
		"jti":  claims.JTI,
	}
	if body.Pod != "" {
		fields["pod"] = body.Pod
	}
	for k, v := range body.Fields {
		fields[k] = v
	}
	s.audit.Emit(audit.Event{
		Category: "argus",
		Outcome:  "broker-event",
		Message:  body.Message,
		Fields:   fields,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decodeJSON(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
