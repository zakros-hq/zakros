package core

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	ghverify "github.com/GoodOlClint/daedalus/cerberus/verification/github"
	hermescore "github.com/GoodOlClint/daedalus/hermes/core"
	"github.com/GoodOlClint/daedalus/minos/storage"
	"github.com/GoodOlClint/daedalus/pkg/audit"
	"github.com/GoodOlClint/daedalus/pkg/jwt"
)

// routes builds the HTTP handler for Minos's API.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.Handle("POST /tasks", s.requireAdmin(http.HandlerFunc(s.handleCreateTask)))
	mux.Handle("GET /tasks", s.requireAdmin(http.HandlerFunc(s.handleListTasks)))
	mux.Handle("GET /tasks/{id}", s.requireAdmin(http.HandlerFunc(s.handleGetTask)))
	mux.Handle("POST /tasks/{id}/pr", s.requirePodAuth(http.HandlerFunc(s.handleReportPR)))
	mux.HandleFunc("POST /webhooks/github", s.handleGithubWebhook)
	return s.auditMiddleware(mux)
}

// auditMiddleware emits one event per request outcome.
func (s *Server) auditMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.audit.Emit(audit.Event{
			Category: "http",
			Outcome:  outcomeFor(rec.status),
			Fields: map[string]string{
				"method": r.Method,
				"path":   r.URL.Path,
				"status": strconv.Itoa(rec.status),
			},
		})
	})
}

// requirePodAuth gates pod-callback endpoints behind the pod's Minos-minted
// bearer token (composed by Commission into envelope.Capabilities.McpAuthToken).
// The token's subject is "task:<task_id>" and must match the {id} path value,
// so a compromised pod cannot report against another task.
func (s *Server) requirePodAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if bearer == "" || bearer == r.Header.Get("Authorization") {
			writeError(w, http.StatusUnauthorized, "missing or malformed bearer")
			return
		}
		secret, err := s.provider.Resolve(r.Context(), s.cfg.BearerSecretRef)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "resolve bearer secret")
			return
		}
		claims, err := jwt.VerifyBearer(secret.Data, bearer)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid bearer")
			return
		}
		const prefix = "task:"
		if !strings.HasPrefix(claims.Subject, prefix) {
			writeError(w, http.StatusUnauthorized, "subject not task-scoped")
			return
		}
		claimTaskID := strings.TrimPrefix(claims.Subject, prefix)
		if claimTaskID != r.PathValue("id") {
			writeError(w, http.StatusForbidden, "task id mismatch")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireAdmin gates operator-only endpoints behind a bearer token resolved
// via the configured secret provider. Phase 1 posture per
// architecture.md §6 MCP Broker Authentication: shared-secret bearer over
// the trusted Crete bridge; Phase 2 swaps to JWT.
func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if got == "" || got == r.Header.Get("Authorization") {
			writeError(w, http.StatusUnauthorized, "missing or malformed bearer")
			return
		}
		want, err := s.provider.Resolve(r.Context(), s.cfg.AdminTokenRef)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "resolve admin token")
			return
		}
		if subtle.ConstantTimeCompare([]byte(got), want.Data) != 1 {
			writeError(w, http.StatusUnauthorized, "invalid bearer")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var req CommissionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("decode body: %v", err))
		return
	}
	task, err := s.Commission(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, taskResponse(task))
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	var states []storage.State
	if raw := r.URL.Query().Get("state"); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			states = append(states, storage.State(strings.TrimSpace(part)))
		}
	}
	limit := 0
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
	out := make([]map[string]any, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, taskResponse(t))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid task id")
		return
	}
	task, err := s.store.GetTask(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "task not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, taskResponse(task))
}

// reportPRRequest is the body shape pods POST to /tasks/{id}/pr.
type reportPRRequest struct {
	PRURL string `json:"pr_url"`
}

// handleReportPR records the PR URL the pod opened so later GitHub
// webhooks can resolve back to this task.
func (s *Server) handleReportPR(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid task id")
		return
	}
	var body reportPRRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("decode body: %v", err))
		return
	}
	if body.PRURL == "" {
		writeError(w, http.StatusBadRequest, "pr_url required")
		return
	}
	if err := s.store.SetTaskPR(r.Context(), id, body.PRURL); err != nil {
		switch {
		case errors.Is(err, storage.ErrNotFound):
			writeError(w, http.StatusNotFound, "task not found")
		case errors.Is(err, storage.ErrConflict):
			writeError(w, http.StatusConflict, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	s.audit.Emit(audit.Event{
		Category: "task",
		Outcome:  "pr-reported",
		Fields:   map[string]string{"task_id": id.String(), "pr_url": body.PRURL},
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// githubPullRequestEvent is the subset of GitHub's pull_request event
// payload Minos cares about for Phase 1 Slice B lifecycle transitions.
type githubPullRequestEvent struct {
	Action      string `json:"action"`
	PullRequest struct {
		HTMLURL string `json:"html_url"`
		Merged  bool   `json:"merged"`
	} `json:"pull_request"`
}

// handleGithubWebhook authenticates a GitHub webhook delivery, parses the
// event, and drives the associated task's state machine. Only the subset
// of events Phase 1 Slice B needs is handled here; unknown events are
// acknowledged (200) but logged for visibility.
func (s *Server) handleGithubWebhook(w http.ResponseWriter, r *http.Request) {
	secret, err := s.provider.Resolve(r.Context(), s.cfg.GithubWebhookSecretRef)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "resolve webhook secret")
		return
	}
	verifier := ghverify.NewVerifier(secret.Data, s.replayStore)
	event, err := verifier.Verify(r.Context(), r)
	if err != nil {
		switch {
		case errors.Is(err, ghverify.ErrInvalidSignature):
			writeError(w, http.StatusUnauthorized, "invalid signature")
		case errors.Is(err, ghverify.ErrMissingHeader):
			writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, ghverify.ErrReplay):
			// Accept + noop on replay per security.md §2 — GitHub retries
			// on non-2xx, and we've already processed this delivery.
			writeJSON(w, http.StatusOK, map[string]string{"status": "duplicate"})
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	switch event.Type {
	case "pull_request":
		s.handlePullRequestEvent(w, r, event.Body)
	default:
		s.audit.Emit(audit.Event{
			Category: "webhook",
			Outcome:  "unhandled-event",
			Fields:   map[string]string{"type": event.Type, "delivery": event.DeliveryID},
		})
		writeJSON(w, http.StatusOK, map[string]string{"status": "unhandled"})
	}
}

// handlePullRequestEvent dispatches pull_request actions. Phase 1 Slice B
// handles `closed` (merged → completed, unmerged → failed); other actions
// (opened, synchronize, reopened, etc.) are noop until Slice C adds
// respawn on review events.
func (s *Server) handlePullRequestEvent(w http.ResponseWriter, r *http.Request, body []byte) {
	var ev githubPullRequestEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("parse pull_request: %v", err))
		return
	}
	if ev.Action != "closed" {
		s.audit.Emit(audit.Event{
			Category: "webhook",
			Outcome:  "pr-ignored",
			Fields:   map[string]string{"action": ev.Action, "pr": ev.PullRequest.HTMLURL},
		})
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}
	task, err := s.store.FindTaskByPRURL(r.Context(), ev.PullRequest.HTMLURL)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			s.audit.Emit(audit.Event{
				Category: "webhook",
				Outcome:  "pr-no-task",
				Fields:   map[string]string{"pr": ev.PullRequest.HTMLURL},
			})
			writeJSON(w, http.StatusOK, map[string]string{"status": "no-matching-task"})
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// From StateRunning we can transition to completed or failed. If the
	// task is already in a terminal state (e.g. earlier webhook delivery
	// already finalized it), this is a noop.
	if task.State == storage.StateCompleted || task.State == storage.StateFailed {
		writeJSON(w, http.StatusOK, map[string]string{"status": "already-terminal"})
		return
	}
	target := storage.StateFailed
	outcome := "pr-closed"
	if ev.PullRequest.Merged {
		target = storage.StateCompleted
		outcome = "pr-merged"
	}
	if err := s.store.TransitionTask(r.Context(), task.ID, target); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit.Emit(audit.Event{
		Category: "webhook",
		Outcome:  outcome,
		Fields: map[string]string{
			"task_id": task.ID.String(),
			"pr":      ev.PullRequest.HTMLURL,
			"state":   string(target),
		},
	})
	s.postSummary(r.Context(), task, target, ev.PullRequest.HTMLURL)
	writeJSON(w, http.StatusOK, map[string]string{"status": string(target)})
}

// postSummary posts the task-terminal message to the task thread via
// Hermes. Failure is non-fatal — audit records the attempt and the task
// remains in the terminal state the webhook set.
func (s *Server) postSummary(ctx context.Context, task *storage.Task, target storage.State, prURL string) {
	if s.hermes == nil || task.Envelope == nil || task.Envelope.Communication.ThreadRef == "" {
		return
	}
	var content string
	switch target {
	case storage.StateCompleted:
		content = fmt.Sprintf("Task completed — PR merged: %s", prURL)
	case storage.StateFailed:
		content = fmt.Sprintf("Task failed — PR closed without merge: %s", prURL)
	default:
		return
	}
	err := s.hermes.PostToThread(ctx, task.Envelope.Communication.ThreadSurface, task.Envelope.Communication.ThreadRef, hermescore.Message{
		Kind:    hermescore.KindSummary,
		Content: content,
	})
	if err != nil {
		s.audit.Emit(audit.Event{
			Category: "hermes",
			Outcome:  "post-summary-failed",
			Message:  err.Error(),
			Fields:   map[string]string{"task_id": task.ID.String(), "thread": task.Envelope.Communication.ThreadRef},
		})
	}
}

// taskResponse is the JSON shape the API returns for task records.
func taskResponse(t *storage.Task) map[string]any {
	out := map[string]any{
		"id":         t.ID,
		"project_id": t.ProjectID,
		"task_type":  t.TaskType,
		"backend":    t.Backend,
		"state":      t.State,
		"created_at": t.CreatedAt,
	}
	if t.ParentID != nil {
		out["parent_id"] = *t.ParentID
	}
	if t.StartedAt != nil {
		out["started_at"] = *t.StartedAt
	}
	if t.FinishedAt != nil {
		out["finished_at"] = *t.FinishedAt
	}
	if t.RunID != nil {
		out["run_id"] = *t.RunID
	}
	if t.PodName != nil {
		out["pod_name"] = *t.PodName
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func outcomeFor(status int) string {
	switch {
	case status >= 500:
		return "server-error"
	case status >= 400:
		return "client-error"
	default:
		return "ok"
	}
}

// statusRecorder wraps an http.ResponseWriter to capture the status code
// for audit logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}
