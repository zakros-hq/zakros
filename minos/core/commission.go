package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	hermescore "github.com/GoodOlClint/daedalus/hermes/core"
	"github.com/GoodOlClint/daedalus/minos/dispatch"
	"github.com/GoodOlClint/daedalus/minos/storage"
	"github.com/GoodOlClint/daedalus/pkg/audit"
	"github.com/GoodOlClint/daedalus/pkg/envelope"
	"github.com/GoodOlClint/daedalus/pkg/jwt"
)

// CommissionRequest is the operator- or surface-facing input that drives
// envelope composition. Fields not present are filled from ProjectConfig.
type CommissionRequest struct {
	TaskType   envelope.TaskType  `json:"task_type"`
	Brief      envelope.Brief     `json:"brief"`
	Inputs     json.RawMessage    `json:"inputs"`
	Acceptance json.RawMessage    `json:"acceptance"`
	Execution  ExecutionRequest   `json:"execution"`
	Budget     *envelope.Budget   `json:"budget,omitempty"`
	Parent     *uuid.UUID         `json:"parent,omitempty"`
	Origin     envelope.Origin    `json:"origin"`
}

// ExecutionRequest mirrors envelope.Execution with all fields optional so
// the composer can fall back to project defaults.
type ExecutionRequest struct {
	RepoURL       string                 `json:"repo_url"`
	Branch        string                 `json:"branch"`
	BaseBranch    string                 `json:"base_branch,omitempty"`
	WorkspaceSize envelope.WorkspaceSize `json:"workspace_size,omitempty"`
}

// Commission validates the request, composes the full task envelope, and
// inserts the task into the store in StateQueued. Dispatch is a separate
// step (Slice A task 6) that transitions the task to StateRunning.
func (s *Server) Commission(ctx context.Context, req CommissionRequest) (*storage.Task, error) {
	if req.TaskType == "" {
		return nil, fmt.Errorf("commission: task_type required")
	}
	if req.Brief.Summary == "" {
		return nil, fmt.Errorf("commission: brief.summary required")
	}
	if req.Execution.RepoURL == "" {
		return nil, fmt.Errorf("commission: execution.repo_url required")
	}
	if req.Execution.Branch == "" {
		return nil, fmt.Errorf("commission: execution.branch required")
	}
	// Slice A task types only; Slice B expands when Discord commission intake lands.
	switch req.TaskType {
	case envelope.TaskTypeCode, envelope.TaskTypeInferenceTuning:
		// ok
	default:
		return nil, fmt.Errorf("commission: task_type %q not yet supported in Phase 1", req.TaskType)
	}

	proj := s.cfg.Project
	taskID := uuid.New()

	execution := envelope.Execution{
		RepoURL:       req.Execution.RepoURL,
		Branch:        req.Execution.Branch,
		BaseBranch:    firstNonEmpty(req.Execution.BaseBranch, proj.DefaultBaseBranch),
		WorkspaceSize: firstNonEmptyWorkspace(req.Execution.WorkspaceSize, proj.DefaultWorkspaceSize),
	}

	budget := proj.DefaultBudget
	if req.Budget != nil {
		budget = *req.Budget
	}

	comm := proj.Communication
	// Create the task thread via Hermes when a broker is wired in. When
	// the configured surface has no plugin registered (Slice A posture:
	// no Discord plugin), leave thread_ref empty — CLI-driven flow.
	if s.hermes != nil && comm.ThreadSurface != "" {
		threadRef, err := s.hermes.CreateThread(ctx, comm.ThreadSurface, hermescore.CreateThreadRequest{
			Parent: proj.ThreadParent,
			Title:  fmt.Sprintf("task-%s", taskID.String()[:8]),
			Opener: fmt.Sprintf("Task commissioned: %s", req.Brief.Summary),
		})
		switch {
		case err == nil:
			comm.ThreadRef = threadRef
		case errors.Is(err, hermescore.ErrNoPlugin):
			// Surface plugin not registered — Slice A posture; proceed
			// without a thread_ref. The audit event makes the degradation
			// visible.
			s.audit.Emit(audit.Event{
				Category: "hermes",
				Outcome:  "no-plugin",
				Fields:   map[string]string{"surface": comm.ThreadSurface, "task_id": taskID.String()},
			})
		default:
			return nil, fmt.Errorf("commission: create thread: %w", err)
		}
	}

	capabilities, err := s.composeCapabilities(ctx, taskID, proj)
	if err != nil {
		return nil, fmt.Errorf("commission: compose capabilities: %w", err)
	}

	inputs := req.Inputs
	if len(inputs) == 0 {
		inputs = json.RawMessage(`{}`)
	}
	acceptance := req.Acceptance
	if len(acceptance) == 0 {
		acceptance = json.RawMessage(`{}`)
	}

	env := &envelope.Envelope{
		SchemaVersion: envelope.SchemaVersion,
		ID:            taskID.String(),
		ProjectID:     proj.ID,
		CreatedAt:     s.now().Format(time.RFC3339),
		TaskType:      req.TaskType,
		Backend:       proj.Backend,
		Origin:        req.Origin,
		Brief:         req.Brief,
		Inputs:        inputs,
		Execution:     execution,
		Communication: comm,
		Capabilities:  capabilities,
		Budget:        budget,
		Acceptance:    acceptance,
	}
	if req.Parent != nil {
		pid := req.Parent.String()
		env.ParentID = &pid
	}
	if err := envelope.Validate(env); err != nil {
		return nil, fmt.Errorf("commission: envelope: %w", err)
	}

	task := &storage.Task{
		ID:        taskID,
		ProjectID: proj.ID,
		TaskType:  req.TaskType,
		Backend:   proj.Backend,
		State:     storage.StateQueued,
		Envelope:  env,
		CreatedAt: s.now(),
	}
	if req.Parent != nil {
		parent := *req.Parent
		task.ParentID = &parent
	}
	if err := s.store.InsertTask(ctx, task); err != nil {
		return nil, fmt.Errorf("commission: insert: %w", err)
	}

	if err := s.dispatch(ctx, task); err != nil {
		return task, err
	}
	return task, nil
}

// dispatch builds the pod spec, spawns the pod, and records the run against
// the task. On any failure after InsertTask, the task is transitioned to
// StateFailed so the operator sees why the commission did not produce a
// running pod.
func (s *Server) dispatch(ctx context.Context, task *storage.Task) error {
	runID := uuid.New()
	spec, err := dispatch.BuildPodSpec(ctx, dispatch.BuilderInput{
		Envelope:      task.Envelope,
		TaskID:        task.ID,
		RunID:         runID,
		Namespace:     s.namespace,
		Image:         s.cfg.Project.PluginImage,
		ProjectID:     s.cfg.Project.ID,
		WorkspaceSize: task.Envelope.Execution.WorkspaceSize,
		MinosURL:      s.cfg.MinosPodURL,
		Resolver:      s.provider,
	})
	if err != nil {
		_ = s.store.TransitionTask(ctx, task.ID, storage.StateFailed)
		s.audit.Emit(audit.Event{
			Category: "dispatch",
			Outcome:  "build-failed",
			Message:  err.Error(),
			Fields:   map[string]string{"task_id": task.ID.String()},
		})
		return fmt.Errorf("commission: build pod spec: %w", err)
	}

	if err := s.dispatcher.SpawnPod(ctx, spec); err != nil {
		_ = s.store.TransitionTask(ctx, task.ID, storage.StateFailed)
		s.audit.Emit(audit.Event{
			Category: "dispatch",
			Outcome:  "spawn-failed",
			Message:  err.Error(),
			Fields:   map[string]string{"task_id": task.ID.String(), "pod_name": spec.Name},
		})
		return fmt.Errorf("commission: spawn pod: %w", err)
	}

	if err := s.store.SetTaskRun(ctx, task.ID, runID, spec.Name); err != nil {
		// Pod is up but state is broken — leave it; startup reconciliation
		// adopts or tombstones it. Don't roll back the pod; audit tells us.
		s.audit.Emit(audit.Event{
			Category: "dispatch",
			Outcome:  "setrun-failed",
			Message:  err.Error(),
			Fields:   map[string]string{"task_id": task.ID.String(), "pod_name": spec.Name},
		})
		return fmt.Errorf("commission: record run: %w", err)
	}
	if err := s.store.TransitionTask(ctx, task.ID, storage.StateRunning); err != nil {
		s.audit.Emit(audit.Event{
			Category: "dispatch",
			Outcome:  "transition-failed",
			Message:  err.Error(),
			Fields:   map[string]string{"task_id": task.ID.String(), "pod_name": spec.Name},
		})
		return fmt.Errorf("commission: transition to running: %w", err)
	}
	// Reflect the live state on the in-memory task returned to the caller.
	task.State = storage.StateRunning
	task.RunID = &runID
	pn := spec.Name
	task.PodName = &pn
	now := s.now()
	task.StartedAt = &now
	s.audit.Emit(audit.Event{
		Category: "dispatch",
		Outcome:  "spawned",
		Fields:   map[string]string{"task_id": task.ID.String(), "run_id": runID.String(), "pod_name": spec.Name},
	})
	return nil
}

// composeCapabilities builds the envelope capabilities block for a new pod,
// minting the Phase 1 bearer token that the pod presents to every MCP
// broker it calls.
func (s *Server) composeCapabilities(ctx context.Context, taskID uuid.UUID, proj ProjectConfig) (envelope.Capabilities, error) {
	endpoints := append([]envelope.McpEndpoint(nil), proj.Capabilities.McpEndpoints...)
	injected := append([]envelope.InjectedCredential(nil), proj.Capabilities.InjectedCredentials...)

	audience := make([]string, 0, len(endpoints))
	scopes := make(map[string][]string, len(endpoints))
	for _, ep := range endpoints {
		audience = append(audience, ep.Name)
		scopes[ep.Name] = append(scopes[ep.Name], ep.Scopes...)
	}

	secret, err := s.provider.Resolve(ctx, s.cfg.BearerSecretRef)
	if err != nil {
		return envelope.Capabilities{}, fmt.Errorf("resolve bearer secret %s: %w", s.cfg.BearerSecretRef, err)
	}
	now := s.now()
	claims := jwt.Claims{
		Subject:   "task:" + taskID.String(),
		Issuer:    "minos",
		Audience:  audience,
		IssuedAt:  now,
		Expires:   now.Add(2 * time.Hour),
		JTI:       uuid.NewString(),
		McpScopes: scopes,
	}
	tok, err := jwt.SignBearer(secret.Data, claims)
	if err != nil {
		return envelope.Capabilities{}, fmt.Errorf("sign bearer: %w", err)
	}
	return envelope.Capabilities{
		InjectedCredentials: injected,
		McpEndpoints:        endpoints,
		McpAuthToken:        tok,
	}, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstNonEmptyWorkspace(vals ...envelope.WorkspaceSize) envelope.WorkspaceSize {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return envelope.WorkspaceSmall
}
