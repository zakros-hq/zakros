package memstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/zakros-hq/zakros/minos/storage"
	"github.com/zakros-hq/zakros/minos/storage/memstore"
	"github.com/zakros-hq/zakros/pkg/envelope"
)

func newTask() *storage.Task {
	return &storage.Task{
		ID:        uuid.New(),
		ProjectID: "test-project",
		TaskType:  envelope.TaskTypeCode,
		Backend:   "claude-code",
	}
}

func TestInsertAndGet(t *testing.T) {
	ctx := context.Background()
	s := memstore.New(nil)
	task := newTask()

	if err := s.InsertTask(ctx, task); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := s.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != storage.StateQueued {
		t.Errorf("expected queued state, got %s", got.State)
	}
}

func TestInsertDuplicate(t *testing.T) {
	ctx := context.Background()
	s := memstore.New(nil)
	task := newTask()

	if err := s.InsertTask(ctx, task); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	err := s.InsertTask(ctx, task)
	if !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
}

func TestGetNotFound(t *testing.T) {
	ctx := context.Background()
	s := memstore.New(nil)

	_, err := s.GetTask(ctx, uuid.New())
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestTransitionStateMachine(t *testing.T) {
	ctx := context.Background()
	s := memstore.New(nil)

	cases := []struct {
		name       string
		transitions []storage.State
		wantErr    bool
	}{
		{"queued→running→completed", []storage.State{storage.StateRunning, storage.StateCompleted}, false},
		{"queued→running→failed", []storage.State{storage.StateRunning, storage.StateFailed}, false},
		{"queued→failed", []storage.State{storage.StateFailed}, false},
		{"queued→completed invalid", []storage.State{storage.StateCompleted}, true},
		{"running→queued invalid", []storage.State{storage.StateRunning, storage.StateQueued}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := newTask()
			if err := s.InsertTask(ctx, task); err != nil {
				t.Fatalf("insert: %v", err)
			}
			var lastErr error
			for _, to := range tc.transitions {
				lastErr = s.TransitionTask(ctx, task.ID, to)
				if lastErr != nil {
					break
				}
			}
			if tc.wantErr && lastErr == nil {
				t.Fatal("expected error, got nil")
			}
			if !tc.wantErr && lastErr != nil {
				t.Fatalf("unexpected error: %v", lastErr)
			}
		})
	}
}

func TestTransitionSetsTimestamps(t *testing.T) {
	ctx := context.Background()
	fixed := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	s := memstore.New(func() time.Time { return fixed })

	task := newTask()
	if err := s.InsertTask(ctx, task); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := s.TransitionTask(ctx, task.ID, storage.StateRunning); err != nil {
		t.Fatalf("transition: %v", err)
	}
	got, _ := s.GetTask(ctx, task.ID)
	if got.StartedAt == nil || !got.StartedAt.Equal(fixed) {
		t.Errorf("StartedAt not set: %v", got.StartedAt)
	}
	if got.FinishedAt != nil {
		t.Errorf("FinishedAt should be nil on running, got %v", got.FinishedAt)
	}

	if err := s.TransitionTask(ctx, task.ID, storage.StateCompleted); err != nil {
		t.Fatalf("complete transition: %v", err)
	}
	got, _ = s.GetTask(ctx, task.ID)
	if got.FinishedAt == nil || !got.FinishedAt.Equal(fixed) {
		t.Errorf("FinishedAt not set: %v", got.FinishedAt)
	}
}

func TestListFilterAndOrder(t *testing.T) {
	ctx := context.Background()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var tick time.Time = t0
	advance := func() time.Time {
		tick = tick.Add(time.Minute)
		return tick
	}
	s := memstore.New(advance)

	a := newTask()
	b := newTask()
	c := newTask()
	for _, tk := range []*storage.Task{a, b, c} {
		if err := s.InsertTask(ctx, tk); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	if err := s.TransitionTask(ctx, a.ID, storage.StateRunning); err != nil {
		t.Fatalf("transition: %v", err)
	}

	all, err := s.ListTasks(ctx, nil, 0)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(all))
	}
	// newest first
	if all[0].ID != c.ID || all[2].ID != a.ID {
		t.Errorf("order wrong: %v %v %v", all[0].ID, all[1].ID, all[2].ID)
	}

	running, _ := s.ListTasks(ctx, []storage.State{storage.StateRunning}, 0)
	if len(running) != 1 || running[0].ID != a.ID {
		t.Errorf("running filter failed: %+v", running)
	}

	limited, _ := s.ListTasks(ctx, nil, 1)
	if len(limited) != 1 {
		t.Errorf("limit failed: %d", len(limited))
	}
}

func TestSetAndFindPRURL(t *testing.T) {
	ctx := context.Background()
	s := memstore.New(nil)
	a := newTask()
	b := newTask()
	for _, tk := range []*storage.Task{a, b} {
		if err := s.InsertTask(ctx, tk); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	url := "https://github.com/example/widget/pull/42"
	if err := s.SetTaskPR(ctx, a.ID, url); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := s.FindTaskByPRURL(ctx, url)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got.ID != a.ID {
		t.Errorf("wrong task: %v vs %v", got.ID, a.ID)
	}
	// Same URL on another task should fail with conflict.
	if err := s.SetTaskPR(ctx, b.ID, url); !errors.Is(err, storage.ErrConflict) {
		t.Errorf("expected ErrConflict on duplicate PR URL, got %v", err)
	}
	// Missing URL returns ErrNotFound.
	if _, err := s.FindTaskByPRURL(ctx, "https://example.invalid/pull/1"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestSetTaskRun(t *testing.T) {
	ctx := context.Background()
	s := memstore.New(nil)
	task := newTask()
	if err := s.InsertTask(ctx, task); err != nil {
		t.Fatalf("insert: %v", err)
	}
	runID := uuid.New()
	if err := s.SetTaskRun(ctx, task.ID, runID, "daedalus-pod-1"); err != nil {
		t.Fatalf("set run: %v", err)
	}
	got, _ := s.GetTask(ctx, task.ID)
	if got.RunID == nil || *got.RunID != runID {
		t.Errorf("run ID not set: %v", got.RunID)
	}
	if got.PodName == nil || *got.PodName != "daedalus-pod-1" {
		t.Errorf("pod name not set: %v", got.PodName)
	}
}
