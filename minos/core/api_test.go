package core_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/GoodOlClint/daedalus/minos/core"
	"github.com/GoodOlClint/daedalus/pkg/envelope"
)

// startTestHTTPServer brings up the Minos HTTP API on an httptest server.
// The admin token is "admin-token" per newTestServer.
func startTestHTTPServer(t *testing.T) (*httptest.Server, *core.Server) {
	t.Helper()
	srv := newTestServer(t).server
	ts := httptest.NewServer(core.TestingHTTPHandler(srv))
	t.Cleanup(ts.Close)
	return ts, srv
}

func authedRequest(t *testing.T, method, url string, body any) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
	req, err := http.NewRequestWithContext(context.Background(), method, url, &buf)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer admin-token")
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestHealthzNoAuth(t *testing.T) {
	ts, _ := startTestHTTPServer(t)
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestCreateTaskRequiresBearer(t *testing.T) {
	ts, _ := startTestHTTPServer(t)
	resp, err := http.Post(ts.URL+"/tasks", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestCreateTaskRejectsWrongBearer(t *testing.T) {
	ts, _ := startTestHTTPServer(t)
	req, err := http.NewRequestWithContext(context.Background(), "POST", ts.URL+"/tasks", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	req.Header.Set("Authorization", "Bearer wrong")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestCreateThenGetThenList(t *testing.T) {
	ts, _ := startTestHTTPServer(t)
	req := authedRequest(t, "POST", ts.URL+"/tasks", core.CommissionRequest{
		TaskType:  envelope.TaskTypeCode,
		Brief:     envelope.Brief{Summary: "fix it"},
		Execution: core.ExecutionRequest{RepoURL: "https://github.com/x/y", Branch: "fix/a"},
		Origin:    envelope.Origin{Surface: "internal", RequestID: "cli-1", Requester: "admin"},
	})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var created map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	resp.Body.Close()
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("no id returned")
	}

	getReq := authedRequest(t, "GET", ts.URL+"/tasks/"+id, nil)
	getResp, err := http.DefaultClient.Do(getReq)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", getResp.StatusCode)
	}

	listReq := authedRequest(t, "GET", ts.URL+"/tasks", nil)
	listResp, err := http.DefaultClient.Do(listReq)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	defer listResp.Body.Close()
	var list []map[string]any
	if err := json.NewDecoder(listResp.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 task, got %d", len(list))
	}
}

func TestGetTaskNotFound(t *testing.T) {
	ts, _ := startTestHTTPServer(t)
	req := authedRequest(t, "GET", ts.URL+"/tasks/00000000-0000-0000-0000-000000000000", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

// Smoke-test the Run lifecycle — start, wait a beat, cancel, expect clean shutdown.
func TestRunStartsAndStops(t *testing.T) {
	srv := newTestServer(t).server
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()

	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil && err != context.Canceled {
			t.Errorf("unexpected Run error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop within 5s after cancel")
	}
}
