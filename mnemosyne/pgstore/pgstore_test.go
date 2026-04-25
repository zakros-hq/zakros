package pgstore_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	mcore "github.com/zakros-hq/zakros/mnemosyne/core"
	"github.com/zakros-hq/zakros/mnemosyne/pgstore"
	minospg "github.com/zakros-hq/zakros/minos/storage/pgstore"
)

const dsnEnv = "ZAKROS_TEST_POSTGRES_DSN"

func openTestStore(t *testing.T) *pgstore.Store {
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
	if err := minospg.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE mnemosyne.run_records`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return pgstore.New(pool)
}

func TestStoreAndGetContext(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	taskID := uuid.New()

	for i, summary := range []string{"first run", "second run"} {
		r := &mcore.RunRecord{
			ID:        uuid.New(),
			TaskID:    taskID,
			RunID:     uuid.New(),
			ProjectID: "integration-p",
			TaskType:  "code",
			Outcome:   mcore.OutcomeCompleted,
			Summary:   summary,
			Body:      json.RawMessage(`{}`),
			CreatedAt: now.Add(time.Duration(i) * time.Minute),
		}
		if err := store.StoreRun(ctx, r); err != nil {
			t.Fatalf("store: %v", err)
		}
	}

	c, err := store.GetContext(ctx, "integration-p", "code")
	if err != nil {
		t.Fatalf("get ctx: %v", err)
	}
	if c == nil {
		t.Fatal("nil context despite stored runs")
	}
	if c.PriorRuns != 2 {
		t.Errorf("expected 2 prior runs, got %d", c.PriorRuns)
	}

	runs, err := store.GetRunsForTask(ctx, taskID)
	if err != nil {
		t.Fatalf("get runs: %v", err)
	}
	if len(runs) != 2 {
		t.Errorf("expected 2, got %d", len(runs))
	}
}
