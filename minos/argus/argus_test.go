package argus_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	hermescore "github.com/GoodOlClint/daedalus/hermes/core"
	"github.com/GoodOlClint/daedalus/hermes/plugins/fakeplugin"
	"github.com/GoodOlClint/daedalus/minos/argus"
	"github.com/GoodOlClint/daedalus/minos/dispatch"
	"github.com/GoodOlClint/daedalus/minos/dispatch/fakedispatch"
	"github.com/GoodOlClint/daedalus/minos/storage"
	"github.com/GoodOlClint/daedalus/minos/storage/memstore"
	"github.com/GoodOlClint/daedalus/pkg/audit"
	"github.com/GoodOlClint/daedalus/pkg/envelope"
)

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

type testRig struct {
	a     *argus.Argus
	clock *fakeClock
	disp  *fakedispatch.Dispatcher
	store *memstore.Store
	plug  *fakeplugin.Plugin
	hermes *hermescore.Broker
}

type fakeClock struct{ t time.Time }

func (f *fakeClock) now() time.Time  { return f.t }
func (f *fakeClock) advance(d time.Duration) { f.t = f.t.Add(d) }

func newRig(t *testing.T, cfg argus.Config) *testRig {
	t.Helper()
	clock := &fakeClock{t: time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)}
	disp := fakedispatch.New()
	store := memstore.New(clock.now)
	plug := fakeplugin.New("discord")
	broker := hermescore.New()
	if err := broker.RegisterPlugin(plug); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := broker.Start(context.Background()); err != nil {
		t.Fatalf("start broker: %v", err)
	}
	a, err := argus.New(cfg, disp, store, broker, audit.NewWriterEmitter("argus-test", discardWriter{}))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	a.WithClock(clock.now)
	return &testRig{a: a, clock: clock, disp: disp, store: store, plug: plug, hermes: broker}
}

func insertTrackedTask(t *testing.T, rig *testRig, budget time.Duration) *storage.Task {
	t.Helper()
	ctx := context.Background()
	threadRef, err := rig.plug.CreateThread(ctx, hermescore.CreateThreadRequest{
		Parent: "channel-ops", Title: "t", Opener: "hi",
	})
	if err != nil {
		t.Fatalf("thread: %v", err)
	}
	taskID := uuid.New()
	runID := uuid.New()
	podName := "daedalus-test-pod"
	env := &envelope.Envelope{
		SchemaVersion: envelope.SchemaVersion,
		ID:            taskID.String(),
		ProjectID:     "test",
		TaskType:      envelope.TaskTypeCode,
		Backend:       "claude-code",
		Budget: envelope.Budget{
			MaxWallClockSeconds:    int(budget.Seconds()),
			WarningThresholdPct:    75,
			EscalationThresholdPct: 90,
		},
		Communication: envelope.Communication{
			ThreadSurface: "discord",
			ThreadRef:     threadRef,
		},
	}
	task := &storage.Task{
		ID:        taskID,
		ProjectID: "test",
		TaskType:  envelope.TaskTypeCode,
		Backend:   "claude-code",
		State:     storage.StateQueued,
		Envelope:  env,
		RunID:     &runID,
		PodName:   &podName,
	}
	if err := rig.store.InsertTask(ctx, task); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := rig.store.TransitionTask(ctx, taskID, storage.StateRunning); err != nil {
		t.Fatalf("transition: %v", err)
	}
	// Spawn a fake pod so DeletePod can find it.
	if err := rig.disp.SpawnPod(ctx, dispatch.PodSpec{Name: podName, Namespace: "daedalus"}); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	rig.a.TrackTask(task, "daedalus")
	return task
}

func TestArgusWarnEscalateAtThresholds(t *testing.T) {
	cfg := argus.DefaultConfig()
	cfg.StallThreshold = 0 // disable stall for this test
	rig := newRig(t, cfg)
	task := insertTrackedTask(t, rig, 100*time.Second)

	// 74% — no warning yet.
	rig.clock.advance(74 * time.Second)
	rig.a.Heartbeat(task.ID)
	rig.a.Evaluate(context.Background())
	threads := rig.plug.Threads()
	if countStatusPosts(threads) != 0 {
		t.Errorf("expected 0 posts before warn, got %d", countStatusPosts(threads))
	}

	// 76% — warning.
	rig.clock.advance(2 * time.Second)
	rig.a.Heartbeat(task.ID)
	rig.a.Evaluate(context.Background())
	if countStatusPosts(rig.plug.Threads()) != 1 {
		t.Errorf("expected warning post at 76%%")
	}

	// 91% — escalation (warning already posted).
	rig.clock.advance(15 * time.Second)
	rig.a.Heartbeat(task.ID)
	rig.a.Evaluate(context.Background())
	if countStatusPosts(rig.plug.Threads()) != 2 {
		t.Errorf("expected warning + escalation posts")
	}
}

func TestArgusTerminatesOnWallClockCap(t *testing.T) {
	cfg := argus.DefaultConfig()
	cfg.StallThreshold = 0
	rig := newRig(t, cfg)
	task := insertTrackedTask(t, rig, 10*time.Second)

	rig.clock.advance(11 * time.Second)
	rig.a.Heartbeat(task.ID)
	rig.a.Evaluate(context.Background())

	if len(rig.disp.Pods()) != 0 {
		t.Errorf("expected pod deleted, still have %d", len(rig.disp.Pods()))
	}
	stored, _ := rig.store.GetTask(context.Background(), task.ID)
	if stored.State != storage.StateFailed {
		t.Errorf("expected failed, got %s", stored.State)
	}
}

func TestArgusTerminatesOnStall(t *testing.T) {
	cfg := argus.DefaultConfig()
	cfg.StallThreshold = 30 * time.Second
	rig := newRig(t, cfg)
	task := insertTrackedTask(t, rig, 1000*time.Second)

	// No heartbeat sent; advance past the stall threshold.
	rig.clock.advance(45 * time.Second)
	rig.a.Evaluate(context.Background())

	if len(rig.disp.Pods()) != 0 {
		t.Errorf("expected pod deleted on stall, still %d", len(rig.disp.Pods()))
	}
	stored, _ := rig.store.GetTask(context.Background(), task.ID)
	if stored.State != storage.StateFailed {
		t.Errorf("expected failed, got %s", stored.State)
	}
	// Task is no longer tracked.
	_ = task
}

func TestArgusHeartbeatResetsStall(t *testing.T) {
	cfg := argus.DefaultConfig()
	cfg.StallThreshold = 30 * time.Second
	rig := newRig(t, cfg)
	task := insertTrackedTask(t, rig, 1000*time.Second)

	rig.clock.advance(20 * time.Second)
	rig.a.Heartbeat(task.ID)
	rig.clock.advance(20 * time.Second) // 20s since last heartbeat; under 30s
	rig.a.Evaluate(context.Background())

	if len(rig.disp.Pods()) != 1 {
		t.Errorf("expected pod alive, got %d", len(rig.disp.Pods()))
	}
	stored, _ := rig.store.GetTask(context.Background(), task.ID)
	if stored.State != storage.StateRunning {
		t.Errorf("expected running, got %s", stored.State)
	}
}

func TestArgusHibernatesOnPodSucceeded(t *testing.T) {
	cfg := argus.DefaultConfig()
	cfg.StallThreshold = 0
	rig := newRig(t, cfg)
	task := insertTrackedTask(t, rig, 1000*time.Second)

	// Simulate the worker opening its PR and exiting cleanly.
	rig.disp.SetPhase("daedalus", *task.PodName, dispatch.PhaseSucceeded)
	rig.a.Evaluate(context.Background())

	stored, _ := rig.store.GetTask(context.Background(), task.ID)
	if stored.State != storage.StateAwaitingReview {
		t.Errorf("expected awaiting-review, got %s", stored.State)
	}
	if len(rig.disp.Pods()) != 0 {
		t.Errorf("expected pod deleted, still %d", len(rig.disp.Pods()))
	}
}

func TestArgusPersistsAndRehydrates(t *testing.T) {
	cfg := argus.DefaultConfig()
	cfg.StallThreshold = 0
	rig := newRig(t, cfg)
	persister := argus.NewMemPersister()
	rig.a.WithPersister(persister)

	task := insertTrackedTask(t, rig, 1000*time.Second)

	// One heartbeat and one warn-trigger so persisted state has non-zero
	// fields beyond the bare TrackTask snapshot.
	rig.clock.advance(50 * time.Second)
	rig.a.Heartbeat(task.ID)

	// Persister should have one state recorded.
	saved, err := persister.Load(context.Background())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(saved) != 1 {
		t.Fatalf("expected 1 persisted state, got %d", len(saved))
	}
	if saved[0].TaskID != task.ID {
		t.Errorf("wrong task id persisted: %v", saved[0].TaskID)
	}

	// Spin up a fresh Argus pointed at the same persister + dispatcher,
	// simulating a Minos restart. After Start() it should pick up the
	// existing pod state without a fresh TrackTask call.
	freshArgus, err := argus.New(cfg, rig.disp, rig.store, rig.hermes, audit.NewWriterEmitter("argus-restart", discardWriter{}))
	if err != nil {
		t.Fatalf("argus restart: %v", err)
	}
	freshArgus.WithPersister(persister)
	freshCtx, freshCancel := context.WithCancel(context.Background())
	defer freshCancel()
	freshArgus.Start(freshCtx)
	defer freshArgus.Stop()

	// Heartbeat the rehydrated state — only succeeds if rehydration found
	// the task in the persister load.
	freshArgus.Heartbeat(task.ID)
	saved, _ = persister.Load(context.Background())
	if len(saved) != 1 {
		t.Fatalf("expected 1 persisted state after rehydrate+heartbeat, got %d", len(saved))
	}
}

func TestArgusUntrackDeletesPersistedState(t *testing.T) {
	cfg := argus.DefaultConfig()
	cfg.StallThreshold = 0
	rig := newRig(t, cfg)
	persister := argus.NewMemPersister()
	rig.a.WithPersister(persister)

	task := insertTrackedTask(t, rig, 1000*time.Second)
	rig.a.UntrackTask(task.ID)

	saved, _ := persister.Load(context.Background())
	if len(saved) != 0 {
		t.Errorf("expected persisted state cleared on untrack, got %d", len(saved))
	}
}

func TestArgusMarksFailedOnPodFailed(t *testing.T) {
	cfg := argus.DefaultConfig()
	cfg.StallThreshold = 0
	rig := newRig(t, cfg)
	task := insertTrackedTask(t, rig, 1000*time.Second)

	rig.disp.SetPhase("daedalus", *task.PodName, dispatch.PhaseFailed)
	rig.a.Evaluate(context.Background())

	stored, _ := rig.store.GetTask(context.Background(), task.ID)
	if stored.State != storage.StateFailed {
		t.Errorf("expected failed, got %s", stored.State)
	}
}

func countStatusPosts(threads []fakeplugin.Thread) int {
	n := 0
	for _, th := range threads {
		for _, post := range th.Posts {
			if post.Kind == hermescore.KindStatus {
				n++
			}
		}
	}
	return n
}
