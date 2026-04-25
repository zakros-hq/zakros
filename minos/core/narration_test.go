package core_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	hermescore "github.com/zakros-hq/zakros/hermes/core"
	"github.com/zakros-hq/zakros/minos/core"
	"github.com/zakros-hq/zakros/pkg/envelope"
)

// postNarration POSTs to /tasks/{id}/post with pod auth. Returns the
// response so the caller can inspect status.
func postNarration(t *testing.T, ts *httptest.Server, taskID, podToken string, body map[string]string) *http.Response {
	t.Helper()
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(context.Background(), "POST", ts.URL+"/tasks/"+taskID+"/post", bytes.NewReader(buf))
	req.Header.Set("Authorization", "Bearer "+podToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	return resp
}

func TestNarrationStatusPostsToThread(t *testing.T) {
	kit, plug := newTestServerWithHermes(t)
	ts := httptest.NewServer(core.TestingHTTPHandler(kit.server))
	defer ts.Close()

	task, err := kit.server.Commission(context.Background(), core.CommissionRequest{
		TaskType:  envelope.TaskTypeCode,
		Brief:     envelope.Brief{Summary: "narrate"},
		Execution: core.ExecutionRequest{RepoURL: "https://example.com/r", Branch: "fix/a"},
	})
	if err != nil {
		t.Fatalf("commission: %v", err)
	}
	resp := postNarration(t, ts, task.ID.String(), task.Envelope.Capabilities.McpAuthToken, map[string]string{
		"kind":    "status",
		"content": "cloning repo",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	found := false
	for _, th := range plug.Threads() {
		for _, p := range th.Posts {
			if p.Kind == hermescore.KindStatus && p.Content == "cloning repo" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected status post on the task thread")
	}
}

func TestNarrationCodeBlockCarriesLanguage(t *testing.T) {
	kit, plug := newTestServerWithHermes(t)
	ts := httptest.NewServer(core.TestingHTTPHandler(kit.server))
	defer ts.Close()

	task, _ := kit.server.Commission(context.Background(), core.CommissionRequest{
		TaskType:  envelope.TaskTypeCode,
		Brief:     envelope.Brief{Summary: "code"},
		Execution: core.ExecutionRequest{RepoURL: "https://example.com/r", Branch: "fix/a"},
	})

	resp := postNarration(t, ts, task.ID.String(), task.Envelope.Capabilities.McpAuthToken, map[string]string{
		"kind":     "code",
		"content":  "fmt.Println(\"hi\")",
		"language": "go",
	})
	defer resp.Body.Close()

	var gotCode, gotLang bool
	for _, th := range plug.Threads() {
		for _, p := range th.Posts {
			if p.Kind == hermescore.KindCode && p.Content == "fmt.Println(\"hi\")" {
				gotCode = true
				if p.Language == "go" {
					gotLang = true
				}
			}
		}
	}
	if !gotCode || !gotLang {
		t.Errorf("expected code+language on post (gotCode=%v gotLang=%v)", gotCode, gotLang)
	}
}

func TestNarrationUnknownKindFallsToStatus(t *testing.T) {
	kit, plug := newTestServerWithHermes(t)
	ts := httptest.NewServer(core.TestingHTTPHandler(kit.server))
	defer ts.Close()

	task, _ := kit.server.Commission(context.Background(), core.CommissionRequest{
		TaskType:  envelope.TaskTypeCode,
		Brief:     envelope.Brief{Summary: "unk"},
		Execution: core.ExecutionRequest{RepoURL: "https://example.com/r", Branch: "fix/a"},
	})

	resp := postNarration(t, ts, task.ID.String(), task.Envelope.Capabilities.McpAuthToken, map[string]string{
		"kind":    "bogus-kind",
		"content": "falls back",
	})
	defer resp.Body.Close()

	var fellBack bool
	for _, th := range plug.Threads() {
		for _, p := range th.Posts {
			if p.Kind == hermescore.KindStatus && p.Content == "falls back" {
				fellBack = true
			}
		}
	}
	if !fellBack {
		t.Errorf("expected unknown kind to fall back to status")
	}
}

func TestNarrationEmptyContentRejected(t *testing.T) {
	kit, _ := newTestServerWithHermes(t)
	ts := httptest.NewServer(core.TestingHTTPHandler(kit.server))
	defer ts.Close()

	task, _ := kit.server.Commission(context.Background(), core.CommissionRequest{
		TaskType:  envelope.TaskTypeCode,
		Brief:     envelope.Brief{Summary: "empty"},
		Execution: core.ExecutionRequest{RepoURL: "https://example.com/r", Branch: "fix/a"},
	})

	resp := postNarration(t, ts, task.ID.String(), task.Envelope.Capabilities.McpAuthToken, map[string]string{
		"kind":    "status",
		"content": "",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for empty content, got %d", resp.StatusCode)
	}
}

func TestNarrationWrongPodForbidden(t *testing.T) {
	kit, _ := newTestServerWithHermes(t)
	ts := httptest.NewServer(core.TestingHTTPHandler(kit.server))
	defer ts.Close()

	taskA, _ := kit.server.Commission(context.Background(), core.CommissionRequest{
		TaskType:  envelope.TaskTypeCode,
		Brief:     envelope.Brief{Summary: "A"},
		Execution: core.ExecutionRequest{RepoURL: "https://example.com/r", Branch: "fix/a"},
	})
	taskB, _ := kit.server.Commission(context.Background(), core.CommissionRequest{
		TaskType:  envelope.TaskTypeCode,
		Brief:     envelope.Brief{Summary: "B"},
		Execution: core.ExecutionRequest{RepoURL: "https://example.com/r", Branch: "fix/b"},
	})

	// Task A's pod token trying to post against task B.
	resp := postNarration(t, ts, taskB.ID.String(), taskA.Envelope.Capabilities.McpAuthToken, map[string]string{
		"kind":    "status",
		"content": "cross-pod attempt",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403, got %d", resp.StatusCode)
	}
}
