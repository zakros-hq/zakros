package replay_test

import (
	"context"
	"testing"
	"time"

	"github.com/zakros-hq/zakros/cerberus/core/replay"
)

func TestMemStoreDedup(t *testing.T) {
	s := replay.NewMemStore(0)
	ctx := context.Background()
	now := time.Now().UTC()

	seen, err := s.Seen(ctx, "github|d-1", now)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if seen {
		t.Error("first insert should not be seen")
	}
	seen, err = s.Seen(ctx, "github|d-1", now)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !seen {
		t.Error("duplicate insert should be seen")
	}
}

func TestMemStoreGC(t *testing.T) {
	s := replay.NewMemStore(10 * time.Minute)
	ctx := context.Background()
	t0 := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)

	// Populate with an old delivery.
	if _, err := s.Seen(ctx, "old", t0); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Jump past the retention window; re-inserting "old" should succeed
	// (gc'd out of the window) rather than be flagged as duplicate.
	seen, err := s.Seen(ctx, "old", t0.Add(time.Hour))
	if err != nil {
		t.Fatalf("after-gc: %v", err)
	}
	if seen {
		t.Error("expected gc to have removed entry outside window")
	}
}
