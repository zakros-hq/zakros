package core

import (
	"crypto/subtle"
	"net/http"
	"strconv"
	"strings"

	"github.com/zakros-hq/zakros/minos/storage"
)

// requireIrisAuth gates the read-only state API and the Hermes pull
// endpoints behind the operator-configured Iris bearer (Config.IrisTokenRef).
// Phase 1 posture mirrors requireAdmin: shared-secret exact match. Phase 2
// (Slice F) replaces this with a Minos-minted JWT scoped to the Iris pod's
// `minos.query_state` and `hermes.{events.next,post_as_iris}` capabilities.
//
// Returns 503 when IrisTokenRef is unset so the routes fail-loud rather
// than silently accepting any bearer when Iris isn't deployed.
func (s *Server) requireIrisAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.IrisTokenRef == "" {
			writeError(w, http.StatusServiceUnavailable, "iris not configured")
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if got == "" || got == r.Header.Get("Authorization") {
			writeError(w, http.StatusUnauthorized, "missing or malformed bearer")
			return
		}
		want, err := s.provider.Resolve(r.Context(), s.cfg.IrisTokenRef)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "resolve iris token")
			return
		}
		if subtle.ConstantTimeCompare([]byte(got), want.Data) != 1 {
			writeError(w, http.StatusUnauthorized, "invalid bearer")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// handleStateTasks returns all tasks, optionally filtered by ?state=<csv>
// and capped by ?limit=<n>. Default limit when unset is 50; pass limit=0
// for unbounded. Mirrors handleListTasks's shape so Iris's tool layer can
// consume it the same way the operator CLI does.
func (s *Server) handleStateTasks(w http.ResponseWriter, r *http.Request) {
	var states []storage.State
	if raw := r.URL.Query().Get("state"); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			p := strings.TrimSpace(part)
			if p == "" {
				continue
			}
			states = append(states, storage.State(p))
		}
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = n
	}
	tasks, err := s.store.ListTasks(r.Context(), states, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, taskListResponse(tasks))
}

// handleStateQueue returns tasks in StateQueued, newest first. Convenience
// over /state/tasks?state=queued for Iris's "what's queued?" intent.
func (s *Server) handleStateQueue(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.store.ListTasks(r.Context(), []storage.State{storage.StateQueued}, 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, taskListResponse(tasks))
}

// handleStateRecent returns recently terminal (completed | failed) tasks,
// newest first. Default 20; ?limit=<n> overrides. Convenience over
// /state/tasks?state=completed,failed for Iris's "what just finished?".
func (s *Server) handleStateRecent(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = n
	}
	tasks, err := s.store.ListTasks(r.Context(),
		[]storage.State{storage.StateCompleted, storage.StateFailed}, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, taskListResponse(tasks))
}

// taskListResponse renders a slice of tasks into the same JSON shape as
// handleListTasks. Lifted out so all three state endpoints share it.
func taskListResponse(tasks []*storage.Task) []map[string]any {
	out := make([]map[string]any, 0, len(tasks))
	for _, t := range tasks {
		entry := taskResponse(t)
		// Surface the brief summary so Iris doesn't need a follow-up GET to
		// render "what's running?" — the brief is the natural answer.
		if t.Envelope != nil && t.Envelope.Brief.Summary != "" {
			entry["summary"] = t.Envelope.Brief.Summary
		}
		out = append(out, entry)
	}
	return out
}
