package core_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zakros-hq/zakros/minos/core"
	"github.com/zakros-hq/zakros/minos/storage"
	"github.com/zakros-hq/zakros/pkg/envelope"
)

// commissionAndGrabToken drives a task through Commission so the test
// has a valid pod bearer to exercise /tasks/{id}/pr against.
func commissionAndGrabToken(t *testing.T, kit testServerKit) (*storage.Task, string) {
	t.Helper()
	task, err := kit.server.Commission(context.Background(), core.CommissionRequest{
		TaskType:  envelope.TaskTypeCode,
		Brief:     envelope.Brief{Summary: "fix"},
		Execution: core.ExecutionRequest{RepoURL: "https://github.com/example/r", Branch: "fix/a"},
		Origin:    envelope.Origin{Surface: "internal", RequestID: "t-1", Requester: "admin"},
	})
	if err != nil {
		t.Fatalf("commission: %v", err)
	}
	return task, task.Envelope.Capabilities.McpAuthToken
}

func TestReportPRHappyPath(t *testing.T) {
	kit := newTestServer(t)
	task, podToken := commissionAndGrabToken(t, kit)

	ts := httptest.NewServer(core.TestingHTTPHandler(kit.server))
	defer ts.Close()

	body, _ := json.Marshal(map[string]string{"pr_url": "https://github.com/example/r/pull/1"})
	req, _ := http.NewRequestWithContext(context.Background(), "POST", ts.URL+"/tasks/"+task.ID.String()+"/pr", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+podToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	stored, _ := kit.store.GetTask(context.Background(), task.ID)
	if stored.PRURL == nil || *stored.PRURL != "https://github.com/example/r/pull/1" {
		t.Errorf("pr url not stored: %v", stored.PRURL)
	}
}

func TestReportPRWrongTaskID(t *testing.T) {
	kit := newTestServer(t)
	taskA, podTokenA := commissionAndGrabToken(t, kit)
	taskB, _ := commissionAndGrabToken(t, kit)
	_ = taskA

	ts := httptest.NewServer(core.TestingHTTPHandler(kit.server))
	defer ts.Close()

	body, _ := json.Marshal(map[string]string{"pr_url": "https://github.com/example/r/pull/evil"})
	// Task A's pod token trying to report for task B.
	req, _ := http.NewRequestWithContext(context.Background(), "POST", ts.URL+"/tasks/"+taskB.ID.String()+"/pr", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+podTokenA)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 (task id mismatch), got %d", resp.StatusCode)
	}
}

func TestReportPRMissingBearer(t *testing.T) {
	kit := newTestServer(t)
	task, _ := commissionAndGrabToken(t, kit)

	ts := httptest.NewServer(core.TestingHTTPHandler(kit.server))
	defer ts.Close()

	req, _ := http.NewRequestWithContext(context.Background(), "POST", ts.URL+"/tasks/"+task.ID.String()+"/pr", bytes.NewReader([]byte(`{"pr_url":"x"}`)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

// signGithub returns X-Hub-Signature-256 for body under secret.
func signGithub(secret, body []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func TestWebhookPullRequestMerged(t *testing.T) {
	kit := newTestServer(t)
	task, podToken := commissionAndGrabToken(t, kit)

	ts := httptest.NewServer(core.TestingHTTPHandler(kit.server))
	defer ts.Close()

	// Bind a PR URL to the task first.
	prURL := "https://github.com/example/r/pull/42"
	prBody, _ := json.Marshal(map[string]string{"pr_url": prURL})
	prReq, _ := http.NewRequestWithContext(context.Background(), "POST", ts.URL+"/tasks/"+task.ID.String()+"/pr", bytes.NewReader(prBody))
	prReq.Header.Set("Authorization", "Bearer "+podToken)
	prReq.Header.Set("Content-Type", "application/json")
	prResp, err := http.DefaultClient.Do(prReq)
	if err != nil || prResp.StatusCode != http.StatusOK {
		t.Fatalf("bind pr: %v (status=%v)", err, prResp.StatusCode)
	}
	prResp.Body.Close()

	// Compose the pull_request merged webhook.
	payload := []byte(`{"action":"closed","pull_request":{"html_url":"` + prURL + `","merged":true}}`)
	req, _ := http.NewRequestWithContext(context.Background(), "POST", ts.URL+"/webhooks/github", bytes.NewReader(payload))
	req.Header.Set("X-Hub-Signature-256", signGithub(kit.webhookSecret, payload))
	req.Header.Set("X-GitHub-Delivery", "delivery-merge-1")
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("webhook: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	stored, _ := kit.store.GetTask(context.Background(), task.ID)
	if stored.State != storage.StateCompleted {
		t.Errorf("expected completed, got %s", stored.State)
	}
}

func TestWebhookPullRequestClosedWithoutMerge(t *testing.T) {
	kit := newTestServer(t)
	task, podToken := commissionAndGrabToken(t, kit)

	ts := httptest.NewServer(core.TestingHTTPHandler(kit.server))
	defer ts.Close()

	prURL := "https://github.com/example/r/pull/77"
	prBody, _ := json.Marshal(map[string]string{"pr_url": prURL})
	prReq, _ := http.NewRequestWithContext(context.Background(), "POST", ts.URL+"/tasks/"+task.ID.String()+"/pr", bytes.NewReader(prBody))
	prReq.Header.Set("Authorization", "Bearer "+podToken)
	prReq.Header.Set("Content-Type", "application/json")
	prResp, err := http.DefaultClient.Do(prReq)
	if err != nil {
		t.Fatalf("bind pr: %v", err)
	}
	prResp.Body.Close()

	payload := []byte(`{"action":"closed","pull_request":{"html_url":"` + prURL + `","merged":false}}`)
	req, _ := http.NewRequestWithContext(context.Background(), "POST", ts.URL+"/webhooks/github", bytes.NewReader(payload))
	req.Header.Set("X-Hub-Signature-256", signGithub(kit.webhookSecret, payload))
	req.Header.Set("X-GitHub-Delivery", "delivery-close-1")
	req.Header.Set("X-GitHub-Event", "pull_request")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("webhook: %v", err)
	}
	resp.Body.Close()
	stored, _ := kit.store.GetTask(context.Background(), task.ID)
	if stored.State != storage.StateFailed {
		t.Errorf("expected failed, got %s", stored.State)
	}
}

func TestWebhookBadSignature(t *testing.T) {
	kit := newTestServer(t)
	ts := httptest.NewServer(core.TestingHTTPHandler(kit.server))
	defer ts.Close()

	payload := []byte(`{}`)
	req, _ := http.NewRequestWithContext(context.Background(), "POST", ts.URL+"/webhooks/github", bytes.NewReader(payload))
	req.Header.Set("X-Hub-Signature-256", signGithub([]byte("wrong-secret"), payload))
	req.Header.Set("X-GitHub-Delivery", "bad-1")
	req.Header.Set("X-GitHub-Event", "ping")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 on bad signature, got %d", resp.StatusCode)
	}
}

func TestWebhookReplayAccepted(t *testing.T) {
	kit := newTestServer(t)
	ts := httptest.NewServer(core.TestingHTTPHandler(kit.server))
	defer ts.Close()

	payload := []byte(`{"action":"opened","pull_request":{"html_url":"https://example.invalid","merged":false}}`)
	sig := signGithub(kit.webhookSecret, payload)

	for i, expected := range []int{http.StatusOK, http.StatusOK} {
		req, _ := http.NewRequestWithContext(context.Background(), "POST", ts.URL+"/webhooks/github", bytes.NewReader(payload))
		req.Header.Set("X-Hub-Signature-256", sig)
		req.Header.Set("X-GitHub-Delivery", "dup-1")
		req.Header.Set("X-GitHub-Event", "pull_request")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		resp.Body.Close()
		if resp.StatusCode != expected {
			t.Errorf("call %d: expected %d, got %d", i, expected, resp.StatusCode)
		}
	}
}

func TestWebhookIgnoresOtherActions(t *testing.T) {
	kit := newTestServer(t)
	ts := httptest.NewServer(core.TestingHTTPHandler(kit.server))
	defer ts.Close()

	payload := []byte(`{"action":"opened","pull_request":{"html_url":"https://example.invalid","merged":false}}`)
	req, _ := http.NewRequestWithContext(context.Background(), "POST", ts.URL+"/webhooks/github", bytes.NewReader(payload))
	req.Header.Set("X-Hub-Signature-256", signGithub(kit.webhookSecret, payload))
	req.Header.Set("X-GitHub-Delivery", "open-1")
	req.Header.Set("X-GitHub-Event", "pull_request")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}
