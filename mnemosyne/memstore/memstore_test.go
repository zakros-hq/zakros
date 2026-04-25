package memstore_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	mcore "github.com/zakros-hq/zakros/mnemosyne/core"
	"github.com/zakros-hq/zakros/mnemosyne/memstore"
)

func newRun(project string, outcome mcore.Outcome, summary string, at time.Time) *mcore.RunRecord {
	return &mcore.RunRecord{
		ID:        uuid.New(),
		TaskID:    uuid.New(),
		RunID:     uuid.New(),
		ProjectID: project,
		TaskType:  "code",
		Outcome:   outcome,
		Summary:   summary,
		Body:      json.RawMessage(`{}`),
		CreatedAt: at,
	}
}

func TestStoreAndGetContext(t *testing.T) {
	ctx := context.Background()
	s := memstore.New()
	now := time.Now().UTC()

	// Insert three records newest-last to verify sort order in output.
	for i, summary := range []string{"oldest", "middle", "newest"} {
		r := newRun("p1", mcore.OutcomeCompleted, summary, now.Add(time.Duration(i)*time.Minute))
		if err := s.StoreRun(ctx, r); err != nil {
			t.Fatalf("store: %v", err)
		}
	}
	// A run for a different project — should not bleed into p1 context.
	if err := s.StoreRun(ctx, newRun("p2", mcore.OutcomeCompleted, "do-not-leak", now)); err != nil {
		t.Fatalf("store p2: %v", err)
	}

	c, err := s.GetContext(ctx, "p1", "code")
	if err != nil {
		t.Fatalf("ctx: %v", err)
	}
	if c.PriorRuns != 3 {
		t.Errorf("prior runs: %d", c.PriorRuns)
	}
	if !strings.Contains(c.Body, "newest") {
		t.Errorf("body missing newest: %s", c.Body)
	}
	if strings.Contains(c.Body, "do-not-leak") {
		t.Errorf("p2 content leaked into p1 context: %s", c.Body)
	}
	newestIdx := strings.Index(c.Body, "newest")
	oldestIdx := strings.Index(c.Body, "oldest")
	if newestIdx < 0 || oldestIdx < 0 || newestIdx > oldestIdx {
		t.Errorf("expected newest before oldest in assembled body")
	}
}

func TestGetContextEmpty(t *testing.T) {
	c, err := memstore.New().GetContext(context.Background(), "empty", "code")
	if err != nil {
		t.Fatalf("ctx: %v", err)
	}
	if c != nil {
		t.Errorf("expected nil context, got %+v", c)
	}
}

func TestGetContextRespectsMax(t *testing.T) {
	ctx := context.Background()
	s := memstore.New()
	s.MaxContextRuns = 2
	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		if err := s.StoreRun(ctx, newRun("p", mcore.OutcomeCompleted, "s", now.Add(time.Duration(i)*time.Second))); err != nil {
			t.Fatalf("store: %v", err)
		}
	}
	c, _ := s.GetContext(ctx, "p", "code")
	if c.PriorRuns != 2 {
		t.Errorf("expected 2, got %d", c.PriorRuns)
	}
}

func TestGetRunsForTask(t *testing.T) {
	ctx := context.Background()
	s := memstore.New()
	taskID := uuid.New()
	// Two runs against the same task (hibernate → respawn scenario).
	now := time.Now().UTC()
	for i := 0; i < 2; i++ {
		r := newRun("p", mcore.OutcomeCompleted, "s", now.Add(time.Duration(i)*time.Second))
		r.TaskID = taskID
		if err := s.StoreRun(ctx, r); err != nil {
			t.Fatalf("store: %v", err)
		}
	}
	// An unrelated task.
	if err := s.StoreRun(ctx, newRun("p", mcore.OutcomeCompleted, "other", now)); err != nil {
		t.Fatalf("store: %v", err)
	}
	runs, err := s.GetRunsForTask(ctx, taskID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(runs) != 2 {
		t.Errorf("expected 2, got %d", len(runs))
	}
}
