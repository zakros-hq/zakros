package core_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/zakros-hq/zakros/minos/core"
	"github.com/zakros-hq/zakros/pkg/envelope"
)

// irisAuthedRequest builds a request bearing the Iris token used by
// newTestServer's static provider ("iris-token").
func irisAuthedRequest(t *testing.T, method, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer iris-token")
	return req
}

func TestStateRequiresIrisBearer(t *testing.T) {
	ts, _ := startTestHTTPServer(t)
	for _, path := range []string{"/state/tasks", "/state/queue", "/state/recent"} {
		t.Run(path+"-no-bearer", func(t *testing.T) {
			resp, err := http.Get(ts.URL + path)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("expected 401, got %d", resp.StatusCode)
			}
		})
		t.Run(path+"-admin-bearer-rejected", func(t *testing.T) {
			// The admin token must not work against Iris-scoped routes —
			// rotation domains are distinct.
			req := authedRequest(t, "GET", ts.URL+path, nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("do: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("expected 401, got %d", resp.StatusCode)
			}
		})
	}
}

func TestStateTasksReturnsCommissioned(t *testing.T) {
	ts, _ := startTestHTTPServer(t)

	req := authedRequest(t, "POST", ts.URL+"/tasks", core.CommissionRequest{
		TaskType:  envelope.TaskTypeCode,
		Brief:     envelope.Brief{Summary: "fix bug 456"},
		Execution: core.ExecutionRequest{RepoURL: "https://github.com/x/y", Branch: "fix/x"},
		Origin:    envelope.Origin{Surface: "internal", RequestID: "iris-1", Requester: "admin"},
	})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	listResp, err := http.DefaultClient.Do(irisAuthedRequest(t, "GET", ts.URL+"/state/tasks"))
	if err != nil {
		t.Fatalf("state/tasks: %v", err)
	}
	defer listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", listResp.StatusCode)
	}
	var list []map[string]any
	if err := json.NewDecoder(listResp.Body).Decode(&list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 task, got %d", len(list))
	}
	if got, _ := list[0]["summary"].(string); got != "fix bug 456" {
		t.Errorf("summary not surfaced: got %q", got)
	}
}

func TestStateRecentEmptyForNonTerminal(t *testing.T) {
	ts, _ := startTestHTTPServer(t)

	req := authedRequest(t, "POST", ts.URL+"/tasks", core.CommissionRequest{
		TaskType:  envelope.TaskTypeCode,
		Brief:     envelope.Brief{Summary: "queued work"},
		Execution: core.ExecutionRequest{RepoURL: "https://github.com/x/y", Branch: "fix/q"},
		Origin:    envelope.Origin{Surface: "internal", RequestID: "iris-q", Requester: "admin"},
	})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()

	recentResp, err := http.DefaultClient.Do(irisAuthedRequest(t, "GET", ts.URL+"/state/recent"))
	if err != nil {
		t.Fatalf("state/recent: %v", err)
	}
	defer recentResp.Body.Close()
	var recent []map[string]any
	_ = json.NewDecoder(recentResp.Body).Decode(&recent)
	if len(recent) != 0 {
		t.Errorf("expected 0 recent terminal tasks, got %d", len(recent))
	}
}
