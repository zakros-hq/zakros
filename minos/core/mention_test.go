package core_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/GoodOlClint/daedalus/minos/core"
	"github.com/GoodOlClint/daedalus/minos/storage"
)

// bindAndHibernate binds a PR URL to a commissioned task and flips the
// state to awaiting-review — the precondition every @mention respawn
// test needs.
func bindAndHibernate(t *testing.T, kit testServerKit, ts *httptest.Server, prURL string) *storage.Task {
	t.Helper()
	task, podToken := commissionAndGrabToken(t, kit)

	body, _ := json.Marshal(map[string]string{"pr_url": prURL})
	req, _ := http.NewRequestWithContext(context.Background(), "POST", ts.URL+"/tasks/"+task.ID.String()+"/pr", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+podToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("bind pr: %v", err)
	}
	resp.Body.Close()

	if err := kit.store.TransitionTask(context.Background(), task.ID, storage.StateAwaitingReview); err != nil {
		t.Fatalf("transition: %v", err)
	}
	// Free the pod so the respawn test has a clean dispatcher slot.
	_ = kit.dispatcher.DeletePod(context.Background(), "daedalus", *task.PodName)
	return task
}

func postIssueComment(t *testing.T, kit testServerKit, ts *httptest.Server, prURL, author, bodyText string) *http.Response {
	t.Helper()
	payload := []byte(`{
      "action": "created",
      "issue": {"pull_request": {"html_url": "` + prURL + `"}},
      "comment": {"body": ` + jsonString(bodyText) + `, "user": {"login": "` + author + `"}}
    }`)
	req, _ := http.NewRequestWithContext(context.Background(), "POST", ts.URL+"/webhooks/github", bytes.NewReader(payload))
	req.Header.Set("X-Hub-Signature-256", signGithub(kit.webhookSecret, payload))
	req.Header.Set("X-GitHub-Delivery", "ic-"+author+"-"+bodyText)
	req.Header.Set("X-GitHub-Event", "issue_comment")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("webhook: %v", err)
	}
	return resp
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestMentionRespawnsTask(t *testing.T) {
	kit, _ := newTestServerWithHermes(t)
	ts := httptest.NewServer(core.TestingHTTPHandler(kit.server))
	defer ts.Close()

	prURL := "https://github.com/example/r/pull/5"
	task := bindAndHibernate(t, kit, ts, prURL)
	firstPod := *task.PodName

	resp := postIssueComment(t, kit, ts, prURL, "reviewer", "hey @daedalus-bot can you add logging")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	stored, _ := kit.store.GetTask(context.Background(), task.ID)
	if stored.State != storage.StateRunning {
		t.Errorf("expected running after mention respawn, got %s", stored.State)
	}
	if stored.PodName == nil || *stored.PodName == firstPod {
		t.Errorf("expected fresh pod after respawn, got %v", stored.PodName)
	}
}

func TestMentionIgnoredWithoutHandle(t *testing.T) {
	kit, _ := newTestServerWithHermes(t)
	ts := httptest.NewServer(core.TestingHTTPHandler(kit.server))
	defer ts.Close()

	prURL := "https://github.com/example/r/pull/6"
	task := bindAndHibernate(t, kit, ts, prURL)

	// Comment doesn't @mention anyone.
	resp := postIssueComment(t, kit, ts, prURL, "reviewer", "looks good, please merge")
	defer resp.Body.Close()

	stored, _ := kit.store.GetTask(context.Background(), task.ID)
	if stored.State != storage.StateAwaitingReview {
		t.Errorf("expected still awaiting-review, got %s", stored.State)
	}
}

func TestMentionIgnoredFromSelf(t *testing.T) {
	kit, _ := newTestServerWithHermes(t)
	ts := httptest.NewServer(core.TestingHTTPHandler(kit.server))
	defer ts.Close()

	prURL := "https://github.com/example/r/pull/7"
	task := bindAndHibernate(t, kit, ts, prURL)

	// The agent's own comment mentioning itself — must not loop.
	resp := postIssueComment(t, kit, ts, prURL, "daedalus-bot", "@daedalus-bot summary posted above")
	defer resp.Body.Close()

	stored, _ := kit.store.GetTask(context.Background(), task.ID)
	if stored.State != storage.StateAwaitingReview {
		t.Errorf("expected still awaiting-review (self-mention ignored), got %s", stored.State)
	}
}

func TestMentionIgnoredOnPlainIssue(t *testing.T) {
	kit, _ := newTestServerWithHermes(t)
	ts := httptest.NewServer(core.TestingHTTPHandler(kit.server))
	defer ts.Close()

	// No pull_request field on the issue — not a PR comment.
	payload := []byte(`{
      "action": "created",
      "issue": {},
      "comment": {"body": "@daedalus-bot please fix", "user": {"login": "reviewer"}}
    }`)
	req, _ := http.NewRequestWithContext(context.Background(), "POST", ts.URL+"/webhooks/github", bytes.NewReader(payload))
	req.Header.Set("X-Hub-Signature-256", signGithub(kit.webhookSecret, payload))
	req.Header.Set("X-GitHub-Delivery", "ic-plain-1")
	req.Header.Set("X-GitHub-Event", "issue_comment")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("webhook: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 (ignored), got %d", resp.StatusCode)
	}
}
