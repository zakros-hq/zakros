package core

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/GoodOlClint/daedalus/pkg/audit"
)

// memoryLookupRequest is the body Iris POSTs to /memory/lookup.
//
// Phase 1 shape: project_id + task_type select the assembled
// last-N-summaries context Minos already injects into fresh task
// envelopes per architecture.md §19. Query is accepted but ignored —
// it lands when Phase 2 grows pgvector semantic lookup. Iris-side
// callers can pass it now so the contract is forward-compatible.
type memoryLookupRequest struct {
	ProjectID string `json:"project_id"`
	TaskType  string `json:"task_type"`
	Query     string `json:"query,omitempty"`
}

// memoryLookupResponse mirrors mnemocore.Context.
type memoryLookupResponse struct {
	Ref       string `json:"ref"`
	Body      string `json:"body"`
	PriorRuns int    `json:"prior_runs"`
}

// handleMemoryLookup serves Iris's `mnemosyne.memory.lookup` MCP scope
// in Phase 1 posture. Returns the assembled per-project prior-run
// context. Empty context (no prior runs) returns 200 with zero-valued
// fields — the client distinguishes via PriorRuns rather than 404, so
// "no memory yet" is a normal state, not an error.
func (s *Server) handleMemoryLookup(w http.ResponseWriter, r *http.Request) {
	if s.mnemosyne == nil {
		writeError(w, http.StatusServiceUnavailable, "mnemosyne not configured")
		return
	}
	var body memoryLookupRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("decode body: %v", err))
		return
	}
	if body.ProjectID == "" || body.TaskType == "" {
		writeError(w, http.StatusBadRequest, "project_id and task_type required")
		return
	}
	ctxBlob, err := s.mnemosyne.GetContext(r.Context(), body.ProjectID, body.TaskType)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := memoryLookupResponse{}
	if ctxBlob != nil {
		resp.Ref = ctxBlob.Ref
		resp.Body = ctxBlob.Body
		resp.PriorRuns = ctxBlob.PriorRuns
	}
	s.audit.Emit(audit.Event{
		Category: "iris",
		Outcome:  "memory-lookup",
		Fields: map[string]string{
			"project_id": body.ProjectID,
			"task_type":  body.TaskType,
		},
	})
	writeJSON(w, http.StatusOK, resp)
}
