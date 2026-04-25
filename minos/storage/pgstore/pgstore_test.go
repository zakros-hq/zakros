package pgstore_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/zakros-hq/zakros/minos/storage"
	"github.com/zakros-hq/zakros/minos/storage/pgstore"
	"github.com/zakros-hq/zakros/pkg/envelope"
)

// dsnEnv names the env var that opts this test suite in. Set to a DSN
// against a disposable Postgres instance (e.g. `make dev-postgres`) to run.
const dsnEnv = "ZAKROS_TEST_POSTGRES_DSN"

func openTestStore(t *testing.T) (*pgstore.Store, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv(dsnEnv)
	if dsn == "" {
		t.Skipf("%s not set; skipping Postgres integration tests", dsnEnv)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := pgstore.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE minos.tasks`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return pgstore.New(pool), pool
}

func newTask() *storage.Task {
	return &storage.Task{
		ID:        uuid.New(),
		ProjectID: "test-project",
		TaskType:  envelope.TaskTypeCode,
		Backend:   "claude-code",
		Envelope: &envelope.Envelope{
			SchemaVersion: envelope.SchemaVersion,
			ID:            uuid.New().String(),
			ProjectID:     "test-project",
			TaskType:      envelope.TaskTypeCode,
			Backend:       "claude-code",
		},
	}
}

func TestInsertGetAndDuplicate(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestStore(t)

	task := newTask()
	if err := store.InsertTask(ctx, task); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != storage.StateQueued {
		t.Errorf("expected queued state, got %s", got.State)
	}
	if got.Envelope == nil || got.Envelope.ID != task.Envelope.ID {
		t.Errorf("envelope round-trip failed: %+v", got.Envelope)
	}

	err = store.InsertTask(ctx, task)
	if !errors.Is(err, storage.ErrConflict) {
		t.Errorf("expected ErrConflict on duplicate, got %v", err)
	}
}

func TestTransitionAndList(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestStore(t)

	a := newTask()
	b := newTask()
	for _, tk := range []*storage.Task{a, b} {
		if err := store.InsertTask(ctx, tk); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	if err := store.TransitionTask(ctx, a.ID, storage.StateRunning); err != nil {
		t.Fatalf("transition a→running: %v", err)
	}
	got, _ := store.GetTask(ctx, a.ID)
	if got.StartedAt == nil {
		t.Errorf("StartedAt not set after transition to running")
	}

	running, err := store.ListTasks(ctx, []storage.State{storage.StateRunning}, 0)
	if err != nil {
		t.Fatalf("list running: %v", err)
	}
	if len(running) != 1 || running[0].ID != a.ID {
		t.Errorf("running filter failed: %+v", running)
	}

	if err := store.TransitionTask(ctx, a.ID, storage.StateQueued); !errors.Is(err, storage.ErrConflict) {
		t.Errorf("expected ErrConflict for running→queued, got %v", err)
	}
}

func TestSetTaskRun(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestStore(t)

	task := newTask()
	if err := store.InsertTask(ctx, task); err != nil {
		t.Fatalf("insert: %v", err)
	}
	runID := uuid.New()
	if err := store.SetTaskRun(ctx, task.ID, runID, "daedalus-pod-1"); err != nil {
		t.Fatalf("set run: %v", err)
	}
	got, _ := store.GetTask(ctx, task.ID)
	if got.RunID == nil || *got.RunID != runID {
		t.Errorf("run ID not set: %v", got.RunID)
	}
	if got.PodName == nil || *got.PodName != "daedalus-pod-1" {
		t.Errorf("pod name not set: %v", got.PodName)
	}
}

func TestSetAndFindPRURL(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestStore(t)

	a := newTask()
	b := newTask()
	for _, tk := range []*storage.Task{a, b} {
		if err := store.InsertTask(ctx, tk); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	url := "https://github.com/example/widget/pull/42"
	if err := store.SetTaskPR(ctx, a.ID, url); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := store.FindTaskByPRURL(ctx, url)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got.ID != a.ID {
		t.Errorf("wrong task: got %v want %v", got.ID, a.ID)
	}
	// Unique constraint on second task with same URL.
	if err := store.SetTaskPR(ctx, b.ID, url); !errors.Is(err, storage.ErrConflict) {
		t.Errorf("expected ErrConflict, got %v", err)
	}
	if _, err := store.FindTaskByPRURL(ctx, "https://example.invalid/pull/1"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestSetTaskRunMissing(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestStore(t)
	err := store.SetTaskRun(ctx, uuid.New(), uuid.New(), "nope")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}
