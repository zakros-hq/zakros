package core

import (
	"context"
	"errors"

	"github.com/zakros-hq/zakros/minos/dispatch"
	"github.com/zakros-hq/zakros/minos/storage"
	"github.com/zakros-hq/zakros/pkg/audit"
)

// Reconcile walks non-terminal tasks in storage and reconciles their
// state against live pod phases. Called from Run() before the HTTP
// listener starts so a restarted Minos doesn't serve traffic against a
// stale view of the Labyrinth.
//
// Phase 1 scope per architecture.md §6 Recovery and Reconciliation:
//   - running task with pod missing → failed (lost during downtime)
//   - running task with pod Succeeded → awaiting-review (pod completed
//     while Minos was down; acts as a late hibernate trigger)
//   - running task with pod Failed → failed
//   - running task with pod Pending/Running → re-adopt (no change)
//   - queued task → left as-is (redispatch is Phase 2)
//   - awaiting-review task → left as-is (webhook-driven)
//
// Reconcile exits on the first storage error. Pod-phase errors are
// tolerated: if the dispatcher cannot be reached we leave the task
// alone rather than transitioning into a worse state than we
// already have.
func (s *Server) Reconcile(ctx context.Context) error {
	tasks, err := s.store.ListTasks(ctx, []storage.State{storage.StateRunning}, 0)
	if err != nil {
		return err
	}
	for _, t := range tasks {
		s.reconcileRunning(ctx, t)
	}
	return nil
}

func (s *Server) reconcileRunning(ctx context.Context, t *storage.Task) {
	if t.PodName == nil || *t.PodName == "" {
		// Running state without a pod name — dispatch path was
		// interrupted before SetTaskRun. Transition to failed so the
		// operator sees the incomplete commission.
		s.transitionReconciled(ctx, t, storage.StateFailed, "no pod name on running task")
		return
	}
	phase, err := s.dispatcher.PodPhase(ctx, s.namespace, *t.PodName)
	if err != nil {
		if errors.Is(err, dispatch.ErrPodNotFound) {
			s.transitionReconciled(ctx, t, storage.StateFailed, "pod missing on restart")
			return
		}
		// Unknown dispatcher error — leave the task alone; try again
		// next restart or rely on Argus's rehydrate path.
		s.audit.Emit(audit.Event{
			Category: "reconcile",
			Outcome:  "pod-phase-error",
			Message:  err.Error(),
			Fields:   map[string]string{"task_id": t.ID.String(), "pod": *t.PodName},
		})
		return
	}
	switch phase {
	case dispatch.PhaseSucceeded:
		s.transitionReconciled(ctx, t, storage.StateAwaitingReview, "pod succeeded during downtime")
	case dispatch.PhaseFailed:
		s.transitionReconciled(ctx, t, storage.StateFailed, "pod failed during downtime")
	case dispatch.PhaseUnknown:
		s.transitionReconciled(ctx, t, storage.StateFailed, "pod phase unknown on restart")
	default:
		// Pending or Running — the pod is still live; leave the row
		// alone. Argus's own rehydrate picks it up via the persister.
		s.audit.Emit(audit.Event{
			Category: "reconcile",
			Outcome:  "adopted",
			Fields:   map[string]string{"task_id": t.ID.String(), "pod": *t.PodName, "phase": string(phase)},
		})
	}
}

func (s *Server) transitionReconciled(ctx context.Context, t *storage.Task, to storage.State, reason string) {
	err := s.store.TransitionTask(ctx, t.ID, to)
	outcome := "transitioned"
	if err != nil && !errors.Is(err, storage.ErrConflict) {
		outcome = "transition-failed"
	}
	s.audit.Emit(audit.Event{
		Category: "reconcile",
		Outcome:  outcome,
		Message:  reason,
		Fields: map[string]string{
			"task_id": t.ID.String(),
			"target":  string(to),
		},
	})
}
