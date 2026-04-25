package core_test

import (
	"context"
	"testing"

	"github.com/zakros-hq/zakros/minos/core"
	"github.com/zakros-hq/zakros/minos/dispatch"
	"github.com/zakros-hq/zakros/minos/storage"
	"github.com/zakros-hq/zakros/pkg/envelope"
)

// commissionRunning returns a task dispatched (StateRunning) through the
// kit's fakedispatch so Reconcile has something to inspect.
func commissionRunning(t *testing.T, kit testServerKit, brief string) *storage.Task {
	t.Helper()
	task, err := kit.server.Commission(context.Background(), core.CommissionRequest{
		TaskType:  envelope.TaskTypeCode,
		Brief:     envelope.Brief{Summary: brief},
		Execution: core.ExecutionRequest{RepoURL: "https://example.com/r", Branch: "fix/a"},
	})
	if err != nil {
		t.Fatalf("commission: %v", err)
	}
	return task
}

func TestReconcileAdoptsLivePods(t *testing.T) {
	kit := newTestServer(t)
	task := commissionRunning(t, kit, "alive")

	// Pod is in Pending (fakedispatch default) — Reconcile should leave
	// the task alone.
	if err := kit.server.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	stored, _ := kit.store.GetTask(context.Background(), task.ID)
	if stored.State != storage.StateRunning {
		t.Errorf("expected still running, got %s", stored.State)
	}
}

func TestReconcileMarksFailedOnMissingPod(t *testing.T) {
	kit := newTestServer(t)
	task := commissionRunning(t, kit, "missing-pod")

	// Simulate a pod that disappeared while Minos was down.
	_ = kit.dispatcher.DeletePod(context.Background(), "zakros", *task.PodName)

	if err := kit.server.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	stored, _ := kit.store.GetTask(context.Background(), task.ID)
	if stored.State != storage.StateFailed {
		t.Errorf("expected failed, got %s", stored.State)
	}
}

func TestReconcileHibernatesSucceededPod(t *testing.T) {
	kit := newTestServer(t)
	task := commissionRunning(t, kit, "succeeded")

	// Simulate the pod completing (PR opened, process exited) while
	// Minos was down — reconcile should move the task to
	// awaiting-review just as Argus would on live observation.
	kit.dispatcher.SetPhase("zakros", *task.PodName, dispatch.PhaseSucceeded)

	if err := kit.server.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	stored, _ := kit.store.GetTask(context.Background(), task.ID)
	if stored.State != storage.StateAwaitingReview {
		t.Errorf("expected awaiting-review, got %s", stored.State)
	}
}

func TestReconcileFailsOnFailedPod(t *testing.T) {
	kit := newTestServer(t)
	task := commissionRunning(t, kit, "pod-failed")
	kit.dispatcher.SetPhase("zakros", *task.PodName, dispatch.PhaseFailed)

	if err := kit.server.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	stored, _ := kit.store.GetTask(context.Background(), task.ID)
	if stored.State != storage.StateFailed {
		t.Errorf("expected failed, got %s", stored.State)
	}
}

func TestReconcileLeavesAwaitingReviewAlone(t *testing.T) {
	kit := newTestServer(t)
	task := commissionRunning(t, kit, "idle")
	// Hibernate the task.
	if err := kit.store.TransitionTask(context.Background(), task.ID, storage.StateAwaitingReview); err != nil {
		t.Fatalf("transition: %v", err)
	}
	// Drop the pod — hibernated tasks have no pod.
	_ = kit.dispatcher.DeletePod(context.Background(), "zakros", *task.PodName)

	if err := kit.server.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	stored, _ := kit.store.GetTask(context.Background(), task.ID)
	if stored.State != storage.StateAwaitingReview {
		t.Errorf("expected still awaiting-review, got %s", stored.State)
	}
}
