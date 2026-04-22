package argus_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/GoodOlClint/daedalus/minos/argus"
)

func makeArgusState(now time.Time) *argus.State {
	return &argus.State{
		TaskID:        uuid.New(),
		RunID:         uuid.New(),
		PodName:       "daedalus-test-pod",
		Namespace:     "daedalus",
		StartedAt:     now,
		LastHeartbeat: now,
		MaxWallClock:  10 * time.Minute,
	}
}

func TestMemPersisterRoundTrip(t *testing.T) {
	p := argus.NewMemPersister()
	ctx := context.Background()

	got, err := p.Load(ctx)
	if err != nil {
		t.Fatalf("load empty: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty load, got %d", len(got))
	}

	t1 := makeArgusState(time.Now())
	t2 := makeArgusState(time.Now().Add(time.Minute))
	for _, st := range []*argus.State{t1, t2} {
		if err := p.Save(ctx, st.TaskID, st); err != nil {
			t.Fatalf("save: %v", err)
		}
	}

	got, err = p.Load(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}

	// Mutating a returned state must not affect the persister's internal copy.
	got[0].Warned = true
	again, _ := p.Load(ctx)
	for _, st := range again {
		if st.TaskID == got[0].TaskID && st.Warned {
			t.Errorf("persister returned a non-cloned reference; in-place mutation leaked")
		}
	}

	if err := p.Delete(ctx, t1.TaskID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, _ = p.Load(ctx)
	if len(got) != 1 || got[0].TaskID != t2.TaskID {
		t.Errorf("expected only t2 remaining, got %+v", got)
	}
}

func TestMemPersisterSaveOverwrite(t *testing.T) {
	p := argus.NewMemPersister()
	ctx := context.Background()
	st := makeArgusState(time.Now())
	if err := p.Save(ctx, st.TaskID, st); err != nil {
		t.Fatalf("save: %v", err)
	}
	st.Warned = true
	if err := p.Save(ctx, st.TaskID, st); err != nil {
		t.Fatalf("re-save: %v", err)
	}
	got, _ := p.Load(ctx)
	if len(got) != 1 || !got[0].Warned {
		t.Errorf("expected single Warned=true state, got %+v", got)
	}
}
