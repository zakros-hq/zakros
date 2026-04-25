package core_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/zakros-hq/zakros/minos/core"
	mnemocore "github.com/zakros-hq/zakros/mnemosyne/core"
)

func TestMemoryLookupRequiresIrisAuth(t *testing.T) {
	kit, _ := newTestServerWithMnemosyne(t)
	ts := httptest.NewServer(core.TestingHTTPHandler(kit.server))
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/memory/lookup", "application/json",
		bytes.NewReader([]byte(`{"project_id":"x","task_type":"code"}`)))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestMemoryLookupEmptyContext(t *testing.T) {
	kit, _ := newTestServerWithMnemosyne(t)
	ts := httptest.NewServer(core.TestingHTTPHandler(kit.server))
	defer ts.Close()

	body, _ := json.Marshal(map[string]string{
		"project_id": "mnemo-project",
		"task_type":  "code",
	})
	req, _ := http.NewRequestWithContext(context.Background(), "POST",
		ts.URL+"/memory/lookup", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer iris-token")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if priorRuns, _ := got["prior_runs"].(float64); priorRuns != 0 {
		t.Errorf("expected 0 prior runs, got %v", priorRuns)
	}
}

func TestMemoryLookupReturnsContext(t *testing.T) {
	kit, mnemo := newTestServerWithMnemosyne(t)
	ts := httptest.NewServer(core.TestingHTTPHandler(kit.server))
	defer ts.Close()

	// Seed a prior run so the assembled context has content.
	if err := mnemo.StoreRun(context.Background(), &mnemocore.RunRecord{
		TaskID:    uuid.New(),
		RunID:     uuid.New(),
		ProjectID: "mnemo-project",
		TaskType:  "code",
		Outcome:   mnemocore.OutcomeCompleted,
		Summary:   "fixed widget bug; touched widget.go and helper_test.go",
		Body:      json.RawMessage(`{"notes":"used helper_test pattern"}`),
	}); err != nil {
		t.Fatalf("store: %v", err)
	}

	body, _ := json.Marshal(map[string]string{
		"project_id": "mnemo-project",
		"task_type":  "code",
		"query":      "anything related to widgets",
	})
	req, _ := http.NewRequestWithContext(context.Background(), "POST",
		ts.URL+"/memory/lookup", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer iris-token")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if priorRuns, _ := got["prior_runs"].(float64); priorRuns < 1 {
		t.Errorf("expected at least 1 prior run, got %v", priorRuns)
	}
	if bodyStr, _ := got["body"].(string); bodyStr == "" {
		t.Error("expected non-empty context body")
	}
}

func TestMemoryLookupRequiresFields(t *testing.T) {
	kit, _ := newTestServerWithMnemosyne(t)
	ts := httptest.NewServer(core.TestingHTTPHandler(kit.server))
	defer ts.Close()

	body, _ := json.Marshal(map[string]string{"project_id": "x"}) // missing task_type
	req, _ := http.NewRequestWithContext(context.Background(), "POST",
		ts.URL+"/memory/lookup", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer iris-token")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}
