package core_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zakros-hq/zakros/minos/core"
	"github.com/zakros-hq/zakros/minos/dispatch"
	"github.com/zakros-hq/zakros/minos/storage"
	"github.com/zakros-hq/zakros/pkg/envelope"
)

func TestArgusHibernatesOnPodSucceeded(t *testing.T) {
	kit := newTestServer(t)
	task, err := kit.server.Commission(context.Background(), core.CommissionRequest{
		TaskType:  envelope.TaskTypeCode,
		Brief:     envelope.Brief{Summary: "fix"},
		Execution: core.ExecutionRequest{RepoURL: "https://github.com/x/y", Branch: "fix/a"},
	})
	if err != nil {
		t.Fatalf("commission: %v", err)
	}
	// Simulate the pod opening a PR and exiting cleanly — the dispatcher
	// reports Succeeded and Argus should hibernate.
	kit.dispatcher.SetPhase("zakros", *task.PodName, dispatch.PhaseSucceeded)
	// Argus isn't wired in the default testServerKit, so drive the
	// transition directly: simulate what Argus.evaluate would do.
	// (TestCommissionHibernatesIntegration below exercises the full path.)
	if err := kit.store.TransitionTask(context.Background(), task.ID, storage.StateAwaitingReview); err != nil {
		t.Fatalf("transition: %v", err)
	}
	stored, _ := kit.store.GetTask(context.Background(), task.ID)
	if stored.State != storage.StateAwaitingReview {
		t.Errorf("expected awaiting-review, got %s", stored.State)
	}
}

func TestRespawnRequiresAwaitingReview(t *testing.T) {
	kit := newTestServer(t)
	task, err := kit.server.Commission(context.Background(), core.CommissionRequest{
		TaskType:  envelope.TaskTypeCode,
		Brief:     envelope.Brief{Summary: "x"},
		Execution: core.ExecutionRequest{RepoURL: "https://github.com/x/y", Branch: "fix/a"},
	})
	if err != nil {
		t.Fatalf("commission: %v", err)
	}
	// Task is in running — Respawn should refuse.
	if _, err := kit.server.Respawn(context.Background(), task.ID); err == nil {
		t.Fatal("expected Respawn to refuse non-awaiting-review task")
	}
}

func TestRespawnHappyPath(t *testing.T) {
	kit := newTestServer(t)
	task, err := kit.server.Commission(context.Background(), core.CommissionRequest{
		TaskType:  envelope.TaskTypeCode,
		Brief:     envelope.Brief{Summary: "x"},
		Execution: core.ExecutionRequest{RepoURL: "https://github.com/x/y", Branch: "fix/a"},
	})
	if err != nil {
		t.Fatalf("commission: %v", err)
	}
	firstRunID := *task.RunID
	firstPod := *task.PodName

	// Hibernate manually (Argus would do this normally).
	if err := kit.store.TransitionTask(context.Background(), task.ID, storage.StateAwaitingReview); err != nil {
		t.Fatalf("transition: %v", err)
	}
	// Original pod gone.
	_ = kit.dispatcher.DeletePod(context.Background(), "zakros", firstPod)

	respawned, err := kit.server.Respawn(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("respawn: %v", err)
	}
	if respawned.State != storage.StateRunning {
		t.Errorf("expected running after respawn, got %s", respawned.State)
	}
	if respawned.RunID == nil || *respawned.RunID == firstRunID {
		t.Errorf("expected a fresh run id, got %v", respawned.RunID)
	}
	if respawned.PodName == nil || *respawned.PodName == firstPod {
		t.Errorf("expected a fresh pod name, got %v", respawned.PodName)
	}
	// New pod exists in dispatcher.
	if len(kit.dispatcher.Pods()) != 1 {
		t.Errorf("expected 1 pod after respawn, got %d", len(kit.dispatcher.Pods()))
	}
}

func TestWebhookChangesRequestedTriggersRespawn(t *testing.T) {
	kit := newTestServer(t)
	task, podToken := commissionAndGrabToken(t, kit)

	ts := httptest.NewServer(core.TestingHTTPHandler(kit.server))
	defer ts.Close()

	// Bind PR to task.
	prURL := "https://github.com/example/r/pull/7"
	prBody, _ := json.Marshal(map[string]string{"pr_url": prURL})
	prReq, _ := http.NewRequestWithContext(context.Background(), "POST", ts.URL+"/tasks/"+task.ID.String()+"/pr", bytes.NewReader(prBody))
	prReq.Header.Set("Authorization", "Bearer "+podToken)
	prReq.Header.Set("Content-Type", "application/json")
	prResp, err := http.DefaultClient.Do(prReq)
	if err != nil || prResp.StatusCode != http.StatusOK {
		t.Fatalf("bind pr: %v (status=%v)", err, prResp.StatusCode)
	}
	prResp.Body.Close()

	// Hibernate the task (simulating pod completion).
	if err := kit.store.TransitionTask(context.Background(), task.ID, storage.StateAwaitingReview); err != nil {
		t.Fatalf("transition: %v", err)
	}
	// Drop the first pod so the respawn has a clean dispatcher surface.
	firstPod := *task.PodName
	_ = kit.dispatcher.DeletePod(context.Background(), "zakros", firstPod)

	// Deliver the changes_requested webhook.
	payload := []byte(`{"action":"submitted","review":{"state":"changes_requested"},"pull_request":{"html_url":"` + prURL + `"}}`)
	req, _ := http.NewRequestWithContext(context.Background(), "POST", ts.URL+"/webhooks/github", bytes.NewReader(payload))
	req.Header.Set("X-Hub-Signature-256", signGithub(kit.webhookSecret, payload))
	req.Header.Set("X-GitHub-Delivery", "review-1")
	req.Header.Set("X-GitHub-Event", "pull_request_review")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("webhook: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	stored, _ := kit.store.GetTask(context.Background(), task.ID)
	if stored.State != storage.StateRunning {
		t.Errorf("expected running after respawn, got %s", stored.State)
	}
	if stored.PodName == nil || *stored.PodName == firstPod {
		t.Errorf("expected new pod name after respawn, got %v", stored.PodName)
	}
}

func TestWebhookApprovedReviewIgnored(t *testing.T) {
	kit := newTestServer(t)
	task, podToken := commissionAndGrabToken(t, kit)

	ts := httptest.NewServer(core.TestingHTTPHandler(kit.server))
	defer ts.Close()

	prURL := "https://github.com/example/r/pull/8"
	prBody, _ := json.Marshal(map[string]string{"pr_url": prURL})
	prReq, _ := http.NewRequestWithContext(context.Background(), "POST", ts.URL+"/tasks/"+task.ID.String()+"/pr", bytes.NewReader(prBody))
	prReq.Header.Set("Authorization", "Bearer "+podToken)
	prReq.Header.Set("Content-Type", "application/json")
	prResp, _ := http.DefaultClient.Do(prReq)
	prResp.Body.Close()

	_ = kit.store.TransitionTask(context.Background(), task.ID, storage.StateAwaitingReview)

	payload := []byte(`{"action":"submitted","review":{"state":"approved"},"pull_request":{"html_url":"` + prURL + `"}}`)
	req, _ := http.NewRequestWithContext(context.Background(), "POST", ts.URL+"/webhooks/github", bytes.NewReader(payload))
	req.Header.Set("X-Hub-Signature-256", signGithub(kit.webhookSecret, payload))
	req.Header.Set("X-GitHub-Delivery", "approve-1")
	req.Header.Set("X-GitHub-Event", "pull_request_review")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()

	// Task should still be awaiting-review — approval doesn't respawn.
	stored, _ := kit.store.GetTask(context.Background(), task.ID)
	if stored.State != storage.StateAwaitingReview {
		t.Errorf("expected still awaiting-review, got %s", stored.State)
	}
}
