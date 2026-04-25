package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	hermescore "github.com/zakros-hq/zakros/hermes/core"
	"github.com/zakros-hq/zakros/minos/storage"
	"github.com/zakros-hq/zakros/pkg/audit"
)

// SweepHibernation runs one pass of the awaiting-review sweeper: reminds
// the admin on tasks past the reminder TTL, abandons (transitions to
// failed) tasks past the abandonment TTL. Exposed so tests and operators
// can trigger an immediate sweep without waiting for the ticker.
func (s *Server) SweepHibernation(ctx context.Context) error {
	reminder, abandon, _, err := s.cfg.Hibernation.Durations()
	if err != nil {
		return err
	}
	// Neither threshold set means nothing to do.
	if reminder == 0 && abandon == 0 {
		return nil
	}

	tasks, err := s.store.ListTasks(ctx, []storage.State{storage.StateAwaitingReview}, 0)
	if err != nil {
		return fmt.Errorf("hibernation sweep: list: %w", err)
	}
	now := s.now()
	for _, t := range tasks {
		dur := now.Sub(t.StateChangedAt)
		switch {
		case abandon > 0 && dur >= abandon:
			s.abandonHibernated(ctx, t, dur)
		case reminder > 0 && dur >= reminder && t.RemindedAt == nil:
			s.remindHibernated(ctx, t, dur)
		}
	}
	return nil
}

// abandonHibernated transitions a stale awaiting-review task to failed
// and posts a thread notice. Transition conflicts are tolerated — another
// worker may have already finalized the task.
func (s *Server) abandonHibernated(ctx context.Context, t *storage.Task, dur time.Duration) {
	err := s.store.TransitionTask(ctx, t.ID, storage.StateFailed)
	if err != nil && !errors.Is(err, storage.ErrConflict) {
		s.audit.Emit(audit.Event{
			Category: "hibernation",
			Outcome:  "abandon-transition-failed",
			Message:  err.Error(),
			Fields:   map[string]string{"task_id": t.ID.String()},
		})
		return
	}
	s.audit.Emit(audit.Event{
		Category: "hibernation",
		Outcome:  "abandoned",
		Fields: map[string]string{
			"task_id":  t.ID.String(),
			"duration": dur.Round(time.Minute).String(),
		},
	})
	s.postThread(ctx, t, fmt.Sprintf(
		"Task abandoned — no review activity in %s. Mark the PR with a review event to respawn.",
		dur.Round(time.Minute),
	))
}

// remindHibernated posts a reminder to the task thread and stamps
// RemindedAt so the next sweep doesn't re-post.
func (s *Server) remindHibernated(ctx context.Context, t *storage.Task, dur time.Duration) {
	s.audit.Emit(audit.Event{
		Category: "hibernation",
		Outcome:  "reminded",
		Fields: map[string]string{
			"task_id":  t.ID.String(),
			"duration": dur.Round(time.Minute).String(),
		},
	})
	s.postThread(ctx, t, fmt.Sprintf(
		"Hibernation reminder — this task has been awaiting review for %s.",
		dur.Round(time.Minute),
	))
	if err := s.store.MarkTaskReminded(ctx, t.ID); err != nil {
		s.audit.Emit(audit.Event{
			Category: "hibernation",
			Outcome:  "mark-reminded-failed",
			Message:  err.Error(),
			Fields:   map[string]string{"task_id": t.ID.String()},
		})
	}
}

// postThread is a best-effort message post to the task's thread via Hermes.
// Silently skipped when Hermes isn't wired or the task has no thread_ref.
func (s *Server) postThread(ctx context.Context, t *storage.Task, content string) {
	if s.hermes == nil || t.Envelope == nil {
		return
	}
	if t.Envelope.Communication.ThreadRef == "" {
		return
	}
	_ = s.hermes.PostToThread(ctx, t.Envelope.Communication.ThreadSurface, t.Envelope.Communication.ThreadRef, hermescore.Message{
		Kind:    hermescore.KindStatus,
		Content: content,
	})
}

// runHibernationSweeper is the background goroutine Run() launches. It
// ticks at the configured sweep interval and calls SweepHibernation on
// each tick. Returns when ctx is cancelled.
func (s *Server) runHibernationSweeper(ctx context.Context) {
	_, _, sweep, err := s.cfg.Hibernation.Durations()
	if err != nil || sweep == 0 {
		return
	}
	ticker := time.NewTicker(sweep)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.SweepHibernation(ctx); err != nil {
				s.audit.Emit(audit.Event{
					Category: "hibernation",
					Outcome:  "sweep-error",
					Message:  err.Error(),
				})
			}
		}
	}
}