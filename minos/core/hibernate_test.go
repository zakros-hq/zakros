package core_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	hermescore "github.com/zakros-hq/zakros/hermes/core"
	"github.com/zakros-hq/zakros/hermes/plugins/fakeplugin"
	"github.com/zakros-hq/zakros/minos/core"
	"github.com/zakros-hq/zakros/minos/storage"
	"github.com/zakros-hq/zakros/pkg/envelope"
)

// ageHibernatedTask rewinds StateChangedAt on a task so the sweeper math
// sees it as stale. Uses the memstore-specific AgeTask helper.
func ageHibernatedTask(t *testing.T, kit testServerKit, id uuid.UUID, ago time.Duration) {
	t.Helper()
	if !kit.store.AgeTask(id, time.Now().Add(-ago)) {
		t.Fatalf("AgeTask: task %s not found", id)
	}
}

// countPostsContaining returns the count of status posts whose content
// contains substr across every thread the plugin knows.
func countPostsContaining(plug *fakeplugin.Plugin, substr string) int {
	n := 0
	for _, th := range plug.Threads() {
		for _, p := range th.Posts {
			if p.Kind == hermescore.KindStatus && strings.Contains(p.Content, substr) {
				n++
			}
		}
	}
	return n
}

func TestHibernationSweepReminds(t *testing.T) {
	kit, plug := newTestServerWithHermes(t)

	task, err := kit.server.Commission(context.Background(), core.CommissionRequest{
		TaskType:  envelope.TaskTypeCode,
		Brief:     envelope.Brief{Summary: "stale"},
		Execution: core.ExecutionRequest{RepoURL: "https://example.com/r", Branch: "fix/a"},
	})
	if err != nil {
		t.Fatalf("commission: %v", err)
	}
	if err := kit.store.TransitionTask(context.Background(), task.ID, storage.StateAwaitingReview); err != nil {
		t.Fatalf("transition: %v", err)
	}
	// Default ReminderAfter in the kit is 24h; age the task by 25h so it
	// crosses the threshold.
	ageHibernatedTask(t, kit, task.ID, 25*time.Hour)

	if err := kit.server.SweepHibernation(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if got := countPostsContaining(plug, "Hibernation reminder"); got != 1 {
		t.Errorf("expected 1 reminder post, got %d", got)
	}

	// A second sweep must not re-post (reminded_at has been stamped).
	if err := kit.server.SweepHibernation(context.Background()); err != nil {
		t.Fatalf("sweep 2: %v", err)
	}
	if got := countPostsContaining(plug, "Hibernation reminder"); got != 1 {
		t.Errorf("expected still 1 reminder after second sweep, got %d", got)
	}
}

func TestHibernationSweepAbandons(t *testing.T) {
	kit, plug := newTestServerWithHermes(t)

	task, err := kit.server.Commission(context.Background(), core.CommissionRequest{
		TaskType:  envelope.TaskTypeCode,
		Brief:     envelope.Brief{Summary: "very stale"},
		Execution: core.ExecutionRequest{RepoURL: "https://example.com/r", Branch: "fix/a"},
	})
	if err != nil {
		t.Fatalf("commission: %v", err)
	}
	if err := kit.store.TransitionTask(context.Background(), task.ID, storage.StateAwaitingReview); err != nil {
		t.Fatalf("transition: %v", err)
	}
	// Default AbandonAfter is 72h; 80h crosses it.
	ageHibernatedTask(t, kit, task.ID, 80*time.Hour)

	if err := kit.server.SweepHibernation(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	stored, _ := kit.store.GetTask(context.Background(), task.ID)
	if stored.State != storage.StateFailed {
		t.Errorf("expected failed after abandonment, got %s", stored.State)
	}
	if got := countPostsContaining(plug, "abandoned"); got == 0 {
		t.Errorf("expected an abandonment post")
	}
}

func TestHibernationSweepSkipsFreshTasks(t *testing.T) {
	kit, plug := newTestServerWithHermes(t)

	task, err := kit.server.Commission(context.Background(), core.CommissionRequest{
		TaskType:  envelope.TaskTypeCode,
		Brief:     envelope.Brief{Summary: "fresh"},
		Execution: core.ExecutionRequest{RepoURL: "https://example.com/r", Branch: "fix/a"},
	})
	if err != nil {
		t.Fatalf("commission: %v", err)
	}
	if err := kit.store.TransitionTask(context.Background(), task.ID, storage.StateAwaitingReview); err != nil {
		t.Fatalf("transition: %v", err)
	}
	// No aging — StateChangedAt is now, well inside the 24h TTL.

	if err := kit.server.SweepHibernation(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if got := countPostsContaining(plug, "reminder"); got != 0 {
		t.Errorf("unexpected reminder for fresh task, got %d", got)
	}
	stored, _ := kit.store.GetTask(context.Background(), task.ID)
	if stored.State != storage.StateAwaitingReview {
		t.Errorf("expected still awaiting-review, got %s", stored.State)
	}
}
