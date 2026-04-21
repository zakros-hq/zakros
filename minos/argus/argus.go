// Package argus is the Phase 1 bundled watcher per architecture.md §7 and
// phase-1-plan.md §6 Slice D. It consumes Argus-sidecar heartbeats and
// enforces a wall-clock cap plus a stall (silence) threshold; when either
// is breached, it terminates the pod via the dispatcher and posts to the
// task thread via Hermes.
//
// Phase 1 scope (per plan):
//   - Heartbeat ingest endpoint (HTTP handler owned by minos/core)
//   - Wall-clock budget from envelope.Budget.MaxWallClockSeconds
//   - Stall threshold from config
//   - Warning at configured percentage, termination at cap
//   - Uses fakedispatch / k3s dispatch to delete pods
//
// Deferred to Phase 2:
//   - Postgres-backed state (current impl is in-memory; state lost on
//     restart)
//   - Non-forgeable token accounting via Apollo
//   - Drift detection
//   - Argus as a standalone service
package argus

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	hermescore "github.com/GoodOlClint/daedalus/hermes/core"
	"github.com/GoodOlClint/daedalus/minos/dispatch"
	"github.com/GoodOlClint/daedalus/minos/storage"
	"github.com/GoodOlClint/daedalus/pkg/audit"
)

// Config controls the rules engine.
type Config struct {
	// StallThreshold is how long a pod may go without a heartbeat before
	// Argus escalates. Zero disables stall detection.
	StallThreshold time.Duration
	// PollInterval is how often the rules engine wakes to re-evaluate
	// every tracked pod. Smaller values react faster; larger values reduce
	// overhead.
	PollInterval time.Duration
	// WarningThresholdPct and EscalationThresholdPct mirror the envelope
	// budget fields — when the task envelope's own thresholds are zero,
	// these defaults apply.
	WarningThresholdPct    int
	EscalationThresholdPct int
}

// DefaultConfig is a reasonable starting point for Phase 1.
func DefaultConfig() Config {
	return Config{
		StallThreshold:         5 * time.Minute,
		PollInterval:           30 * time.Second,
		WarningThresholdPct:    75,
		EscalationThresholdPct: 90,
	}
}

// State records what Argus knows about one active pod.
type State struct {
	TaskID        uuid.UUID
	RunID         uuid.UUID
	PodName       string
	Namespace     string
	ThreadSurface string
	ThreadRef     string
	StartedAt     time.Time
	LastHeartbeat time.Time
	MaxWallClock  time.Duration
	WarningAt     time.Duration // wall-clock at which to emit warning
	EscalationAt  time.Duration // wall-clock at which to escalate
	Warned        bool
	Escalated     bool
	Terminated    bool
}

// Argus is the bundled watcher.
type Argus struct {
	cfg        Config
	dispatcher dispatch.Dispatcher
	store      storage.Store
	hermes     *hermescore.Broker
	audit      audit.Emitter
	now        func() time.Time

	mu    sync.Mutex
	pods  map[uuid.UUID]*State
	stopC chan struct{}
	done  chan struct{}
}

// New constructs an Argus instance. Dispatcher is required so Argus can
// terminate pods; hermes and audit may be nil (warnings and escalations
// are then silently skipped from the user surface but still gated via
// the audit emitter — a nil emitter is safe because core wires a stdout
// one).
func New(cfg Config, d dispatch.Dispatcher, store storage.Store, h *hermescore.Broker, em audit.Emitter) (*Argus, error) {
	if d == nil {
		return nil, errors.New("argus: dispatcher required")
	}
	if store == nil {
		return nil, errors.New("argus: store required")
	}
	if em == nil {
		return nil, errors.New("argus: audit emitter required")
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 30 * time.Second
	}
	return &Argus{
		cfg:        cfg,
		dispatcher: d,
		store:      store,
		hermes:     h,
		audit:      em,
		now:        func() time.Time { return time.Now().UTC() },
		pods:       map[uuid.UUID]*State{},
	}, nil
}

// WithClock overrides the internal clock — used by tests.
func (a *Argus) WithClock(now func() time.Time) *Argus {
	a.now = now
	return a
}

// TrackTask begins tracking a newly-running task. Called by core.Server
// after a successful dispatch.
func (a *Argus) TrackTask(t *storage.Task, namespace string) {
	if t == nil || t.Envelope == nil || t.PodName == nil || t.RunID == nil {
		return
	}
	max := time.Duration(t.Envelope.Budget.MaxWallClockSeconds) * time.Second
	warnPct := t.Envelope.Budget.WarningThresholdPct
	if warnPct == 0 {
		warnPct = a.cfg.WarningThresholdPct
	}
	escPct := t.Envelope.Budget.EscalationThresholdPct
	if escPct == 0 {
		escPct = a.cfg.EscalationThresholdPct
	}
	now := a.now()
	st := &State{
		TaskID:        t.ID,
		RunID:         *t.RunID,
		PodName:       *t.PodName,
		Namespace:     namespace,
		ThreadSurface: t.Envelope.Communication.ThreadSurface,
		ThreadRef:     t.Envelope.Communication.ThreadRef,
		StartedAt:     now,
		LastHeartbeat: now,
		MaxWallClock:  max,
		WarningAt:     (max * time.Duration(warnPct)) / 100,
		EscalationAt:  (max * time.Duration(escPct)) / 100,
	}
	a.mu.Lock()
	a.pods[t.ID] = st
	a.mu.Unlock()
}

// UntrackTask stops tracking a task — called when it transitions to a
// terminal state.
func (a *Argus) UntrackTask(id uuid.UUID) {
	a.mu.Lock()
	delete(a.pods, id)
	a.mu.Unlock()
}

// Heartbeat is called when a sidecar reports it is alive. Unknown task IDs
// are ignored.
func (a *Argus) Heartbeat(id uuid.UUID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	st, ok := a.pods[id]
	if !ok {
		return
	}
	st.LastHeartbeat = a.now()
}

// Start begins the rules engine loop. Stop halts it.
func (a *Argus) Start(ctx context.Context) {
	a.mu.Lock()
	if a.stopC != nil {
		a.mu.Unlock()
		return
	}
	a.stopC = make(chan struct{})
	a.done = make(chan struct{})
	a.mu.Unlock()

	go a.runLoop(ctx)
}

// Stop halts the rules engine. Idempotent; safe if Start was never called.
func (a *Argus) Stop() {
	a.mu.Lock()
	stopC := a.stopC
	done := a.done
	a.stopC = nil
	a.mu.Unlock()
	if stopC == nil {
		return
	}
	close(stopC)
	<-done
}

func (a *Argus) runLoop(ctx context.Context) {
	defer close(a.done)
	ticker := time.NewTicker(a.cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.stopC:
			return
		case <-ticker.C:
			a.evaluate(ctx)
		}
	}
}

// evaluate walks every tracked pod and applies the rules. Visible to tests.
func (a *Argus) evaluate(ctx context.Context) {
	now := a.now()
	a.mu.Lock()
	// Copy into a slice so the map mutation on terminate is safe.
	states := make([]*State, 0, len(a.pods))
	for _, s := range a.pods {
		states = append(states, s)
	}
	a.mu.Unlock()

	for _, st := range states {
		if st.Terminated {
			continue
		}
		wall := now.Sub(st.StartedAt)
		silence := now.Sub(st.LastHeartbeat)

		switch {
		case st.MaxWallClock > 0 && wall >= st.MaxWallClock:
			a.terminate(ctx, st, fmt.Sprintf("wall-clock cap exceeded (%s ≥ %s)", wall.Round(time.Second), st.MaxWallClock))
		case a.cfg.StallThreshold > 0 && silence >= a.cfg.StallThreshold:
			a.terminate(ctx, st, fmt.Sprintf("no heartbeat for %s (threshold %s)", silence.Round(time.Second), a.cfg.StallThreshold))
		case st.MaxWallClock > 0 && !st.Escalated && st.EscalationAt > 0 && wall >= st.EscalationAt:
			a.escalate(ctx, st, fmt.Sprintf("%d%% of wall-clock budget used (%s/%s)", percentOf(wall, st.MaxWallClock), wall.Round(time.Second), st.MaxWallClock))
		case st.MaxWallClock > 0 && !st.Warned && st.WarningAt > 0 && wall >= st.WarningAt:
			a.warn(ctx, st, fmt.Sprintf("%d%% of wall-clock budget used (%s/%s)", percentOf(wall, st.MaxWallClock), wall.Round(time.Second), st.MaxWallClock))
		}
	}
}

func (a *Argus) warn(ctx context.Context, st *State, message string) {
	a.markWarned(st.TaskID)
	a.audit.Emit(audit.Event{Category: "argus", Outcome: "warning",
		Fields: map[string]string{"task_id": st.TaskID.String(), "reason": message}})
	a.postThread(ctx, st, "argus warning: "+message, hermescore.KindStatus)
}

func (a *Argus) escalate(ctx context.Context, st *State, message string) {
	a.markEscalated(st.TaskID)
	a.audit.Emit(audit.Event{Category: "argus", Outcome: "escalation",
		Fields: map[string]string{"task_id": st.TaskID.String(), "reason": message}})
	a.postThread(ctx, st, "argus escalation: "+message, hermescore.KindStatus)
}

func (a *Argus) terminate(ctx context.Context, st *State, reason string) {
	a.markTerminated(st.TaskID)
	a.audit.Emit(audit.Event{Category: "argus", Outcome: "termination",
		Fields: map[string]string{"task_id": st.TaskID.String(), "pod": st.PodName, "reason": reason}})
	a.postThread(ctx, st, "argus termination: "+reason, hermescore.KindStatus)
	if err := a.dispatcher.DeletePod(ctx, st.Namespace, st.PodName); err != nil && !errors.Is(err, dispatch.ErrPodNotFound) {
		a.audit.Emit(audit.Event{Category: "argus", Outcome: "delete-failed",
			Message: err.Error(),
			Fields:  map[string]string{"task_id": st.TaskID.String(), "pod": st.PodName}})
	}
	if err := a.store.TransitionTask(ctx, st.TaskID, storage.StateFailed); err != nil {
		// If the task is already terminal that's fine; log other errors.
		if !errors.Is(err, storage.ErrConflict) {
			a.audit.Emit(audit.Event{Category: "argus", Outcome: "transition-failed",
				Message: err.Error(),
				Fields:  map[string]string{"task_id": st.TaskID.String()}})
		}
	}
	// Drop from active tracking once terminated.
	a.UntrackTask(st.TaskID)
}

func (a *Argus) postThread(ctx context.Context, st *State, text string, kind hermescore.MessageKind) {
	if a.hermes == nil || st.ThreadSurface == "" || st.ThreadRef == "" {
		return
	}
	_ = a.hermes.PostToThread(ctx, st.ThreadSurface, st.ThreadRef, hermescore.Message{
		Kind:    kind,
		Content: text,
	})
}

func (a *Argus) markWarned(id uuid.UUID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if st, ok := a.pods[id]; ok {
		st.Warned = true
	}
}

func (a *Argus) markEscalated(id uuid.UUID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if st, ok := a.pods[id]; ok {
		st.Escalated = true
	}
}

func (a *Argus) markTerminated(id uuid.UUID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if st, ok := a.pods[id]; ok {
		st.Terminated = true
	}
}

func percentOf(elapsed, total time.Duration) int {
	if total == 0 {
		return 0
	}
	return int((elapsed * 100) / total)
}
