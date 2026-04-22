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
	mnemocore "github.com/GoodOlClint/daedalus/mnemosyne/core"
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
	mux.Handle("POST /tasks/{id}/heartbeat", s.requirePodAuth(http.HandlerFunc(s.handleHeartbeat)))
	mux.Handle("POST /tasks/{id}/memory", s.requirePodAuth(http.HandlerFunc(s.handleReportMemory)))
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

// handleHeartbeat accepts Argus-sidecar heartbeat POSTs and forwards the
// task id to the watcher. Unknown task ids are no-ops (Argus ignores
// them), so the endpoint always returns 200 when auth passes.
func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid task id")
		return
	}
	if s.argus != nil {
		s.argus.Heartbeat(id)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// reportMemoryRequest is the body shape pods POST to /tasks/{id}/memory.
type reportMemoryRequest struct {
	Outcome string          `json:"outcome"`
	Summary string          `json:"summary"`
	Body    json.RawMessage `json:"body"`
}

// handleReportMemory persists the pod's run record via Mnemosyne.
// Sanitization runs here against the pod's injected credentials and the
// minted bearer so no credential value lands in the persisted record.
func (s *Server) handleReportMemory(w http.ResponseWriter, r *http.Request) {
	if s.mnemosyne == nil {
		writeError(w, http.StatusServiceUnavailable, "mnemosyne not configured")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid task id")
		return
	}
	var body reportMemoryRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("decode body: %v", err))
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

	known := s.collectKnownSecrets(r.Context(), task)
	sanitized := mnemocore.Sanitize(body.Body, known)

	runID := uuid.Nil
	if task.RunID != nil {
		runID = *task.RunID
	}
	outcome := mnemocore.Outcome(body.Outcome)
	switch outcome {
	case mnemocore.OutcomeCompleted, mnemocore.OutcomeFailed, mnemocore.OutcomeTerminated:
		// ok
	default:
		outcome = mnemocore.OutcomeCompleted
	}

	err = s.mnemosyne.StoreRun(r.Context(), &mnemocore.RunRecord{
		TaskID:    id,
		RunID:     runID,
		ProjectID: task.ProjectID,
		TaskType:  string(task.TaskType),
		Outcome:   outcome,
		Summary:   body.Summary,
		Body:      sanitized,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit.Emit(audit.Event{
		Category: "mnemosyne",
		Outcome:  "stored",
		Fields:   map[string]string{"task_id": id.String(), "outcome": string(outcome)},
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// collectKnownSecrets returns the set of plaintext values that the pod
// had access to, so Sanitize can redact them from the persisted run
// record. Includes the pod's bearer token (if resolvable) plus every
// InjectedCredential the envelope declared.
func (s *Server) collectKnownSecrets(ctx context.Context, task *storage.Task) [][]byte {
	var out [][]byte
	if task == nil || task.Envelope == nil {
		return out
	}
	for _, ic := range task.Envelope.Capabilities.InjectedCredentials {
		if ic.CredentialsRef == "" {
			continue
		}
		v, err := s.provider.Resolve(ctx, ic.CredentialsRef)
		if err == nil && v != nil && len(v.Data) > 0 {
			dup := make([]byte, len(v.Data))
			copy(dup, v.Data)
			out = append(out, dup)
		}
	}
	if tok := task.Envelope.Capabilities.McpAuthToken; tok != "" {
		out = append(out, []byte(tok))
	}
	return out
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
	case "pull_request_review":
		s.handlePullRequestReviewEvent(w, r, event.Body)
	case "issue_comment":
		s.handleIssueCommentEvent(w, r, event.Body)
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
	if s.argus != nil {
		s.argus.UntrackTask(task.ID)
	}
	s.postSummary(r.Context(), task, target, ev.PullRequest.HTMLURL)
	writeJSON(w, http.StatusOK, map[string]string{"status": string(target)})
}

// githubPullRequestReviewEvent is the subset of the pull_request_review
// payload Slice C consumes. Only `submitted` actions with state
// `changes_requested` trigger a respawn.
type githubPullRequestReviewEvent struct {
	Action      string `json:"action"`
	Review      struct {
		State string `json:"state"`
	} `json:"review"`
	PullRequest struct {
		HTMLURL string `json:"html_url"`
	} `json:"pull_request"`
}

// githubIssueCommentEvent is the subset of the issue_comment payload
// Slice C consumes. Only comments on pull requests (not plain issues)
// and only actions of "created" fire the @mention respawn path.
type githubIssueCommentEvent struct {
	Action string `json:"action"`
	Issue  struct {
		PullRequest *struct {
			HTMLURL string `json:"html_url"`
		} `json:"pull_request"`
	} `json:"issue"`
	Comment struct {
		Body string `json:"body"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
	} `json:"comment"`
}

// handleIssueCommentEvent respawns the bound task when a PR comment
// @mentions the configured agent handle. Comments on plain issues (no
// pull_request field) are ignored; same for edits/deletions and
// self-authored comments (the agent's own PR summary).
func (s *Server) handleIssueCommentEvent(w http.ResponseWriter, r *http.Request, body []byte) {
	handle := strings.TrimSpace(s.cfg.Project.AgentMentionHandle)
	if handle == "" {
		writeJSON(w, http.StatusOK, map[string]string{"status": "mention-disabled"})
		return
	}
	var ev githubIssueCommentEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("parse issue_comment: %v", err))
		return
	}
	if ev.Action != "created" || ev.Issue.PullRequest == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}
	// Ignore comments authored by the agent itself — the bot's own PR
	// summary shouldn't trigger respawn.
	if strings.EqualFold(ev.Comment.User.Login, handle) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "self-authored"})
		return
	}
	// Case-insensitive @mention match. GitHub mentions are "@<login>"
	// with word boundaries; the simple contains-check catches the
	// common case without over-matching in code blocks.
	mention := "@" + handle
	if !strings.Contains(strings.ToLower(ev.Comment.Body), strings.ToLower(mention)) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "no-mention"})
		return
	}

	task, err := s.store.FindTaskByPRURL(r.Context(), ev.Issue.PullRequest.HTMLURL)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			s.audit.Emit(audit.Event{
				Category: "webhook",
				Outcome:  "mention-no-task",
				Fields:   map[string]string{"pr": ev.Issue.PullRequest.HTMLURL},
			})
			writeJSON(w, http.StatusOK, map[string]string{"status": "no-matching-task"})
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if task.State != storage.StateAwaitingReview {
		s.audit.Emit(audit.Event{
			Category: "webhook",
			Outcome:  "mention-wrong-state",
			Fields:   map[string]string{"task_id": task.ID.String(), "state": string(task.State)},
		})
		writeJSON(w, http.StatusOK, map[string]string{"status": string(task.State)})
		return
	}
	if _, err := s.Respawn(r.Context(), task.ID); err != nil {
		s.audit.Emit(audit.Event{
			Category: "webhook",
			Outcome:  "mention-respawn-failed",
			Message:  err.Error(),
			Fields:   map[string]string{"task_id": task.ID.String()},
		})
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit.Emit(audit.Event{
		Category: "webhook",
		Outcome:  "mention-respawn",
		Fields:   map[string]string{"task_id": task.ID.String(), "user": ev.Comment.User.Login},
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "respawned"})
}

// handlePullRequestReviewEvent drives hibernation → respawn on qualifying
// review events per phase-1-plan.md §7 Slice C task 5.
func (s *Server) handlePullRequestReviewEvent(w http.ResponseWriter, r *http.Request, body []byte) {
	var ev githubPullRequestReviewEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("parse pull_request_review: %v", err))
		return
	}
	// Only `changes_requested` on a `submitted` review respawns; approvals
	// and plain comments don't trigger a new run.
	if ev.Action != "submitted" || ev.Review.State != "changes_requested" {
		s.audit.Emit(audit.Event{
			Category: "webhook",
			Outcome:  "review-ignored",
			Fields:   map[string]string{"action": ev.Action, "state": ev.Review.State, "pr": ev.PullRequest.HTMLURL},
		})
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}
	task, err := s.store.FindTaskByPRURL(r.Context(), ev.PullRequest.HTMLURL)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			s.audit.Emit(audit.Event{
				Category: "webhook",
				Outcome:  "review-no-task",
				Fields:   map[string]string{"pr": ev.PullRequest.HTMLURL},
			})
			writeJSON(w, http.StatusOK, map[string]string{"status": "no-matching-task"})
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if task.State != storage.StateAwaitingReview {
		// Already running (e.g. concurrent review) or already terminal.
		s.audit.Emit(audit.Event{
			Category: "webhook",
			Outcome:  "review-wrong-state",
			Fields:   map[string]string{"task_id": task.ID.String(), "state": string(task.State)},
		})
		writeJSON(w, http.StatusOK, map[string]string{"status": string(task.State)})
		return
	}
	if _, err := s.Respawn(r.Context(), task.ID); err != nil {
		s.audit.Emit(audit.Event{
			Category: "webhook",
			Outcome:  "respawn-failed",
			Message:  err.Error(),
			Fields:   map[string]string{"task_id": task.ID.String()},
		})
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "respawned"})
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
