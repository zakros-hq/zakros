package iris

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// MinosClient talks to Minos's HTTP API on Iris's behalf. It carries two
// bearers: irisToken for the read-only `/state/*`, `/hermes/*`, and
// `/memory/lookup` routes, and adminToken for the `POST /tasks`
// commission path. Phase 2 Slice F replaces both with a Minos-minted
// JWT scoped to the appropriate broker capabilities.
type MinosClient struct {
	BaseURL    string
	IrisToken  string
	AdminToken string

	HTTPClient *http.Client
}

// NewMinosClient constructs a client. baseURL must NOT end in a slash.
func NewMinosClient(baseURL, irisToken, adminToken string) *MinosClient {
	return &MinosClient{
		BaseURL:    baseURL,
		IrisToken:  irisToken,
		AdminToken: adminToken,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// TaskSummary is the projection of /state/* responses Iris cares about.
// Mirrors the JSON Minos's taskListResponse emits (id + state + summary).
type TaskSummary struct {
	ID         string `json:"id"`
	State      string `json:"state"`
	TaskType   string `json:"task_type"`
	ProjectID  string `json:"project_id"`
	CreatedAt  string `json:"created_at"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
	Summary    string `json:"summary,omitempty"`
	PRURL      string `json:"pr_url,omitempty"`
}

// StateTasks returns active + recent tasks. State filter is a CSV list
// (queued,running,awaiting-review,completed,failed); empty returns all.
func (c *MinosClient) StateTasks(ctx context.Context, stateFilter string, limit int) ([]TaskSummary, error) {
	q := url.Values{}
	if stateFilter != "" {
		q.Set("state", stateFilter)
	}
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	return c.getTaskList(ctx, "/state/tasks?"+q.Encode())
}

// StateQueue returns queued tasks (Minos's "what's queued" convenience).
func (c *MinosClient) StateQueue(ctx context.Context) ([]TaskSummary, error) {
	return c.getTaskList(ctx, "/state/queue")
}

// StateRecent returns recently terminal tasks.
func (c *MinosClient) StateRecent(ctx context.Context, limit int) ([]TaskSummary, error) {
	path := "/state/recent"
	if limit > 0 {
		path = fmt.Sprintf("%s?limit=%d", path, limit)
	}
	return c.getTaskList(ctx, path)
}

// MemoryLookup returns Mnemosyne's assembled prior-run context for a
// project + task type. Phase 1 ignores `query` — accepts it so the
// client contract is forward-compatible with Phase 2 semantic lookup.
type MemoryLookupResponse struct {
	Ref       string `json:"ref"`
	Body      string `json:"body"`
	PriorRuns int    `json:"prior_runs"`
}

func (c *MinosClient) MemoryLookup(ctx context.Context, projectID, taskType, query string) (*MemoryLookupResponse, error) {
	body, _ := json.Marshal(map[string]string{
		"project_id": projectID,
		"task_type":  taskType,
		"query":      query,
	})
	req, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/memory/lookup", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.IrisToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("memory.lookup: %s: %s", resp.Status, readSnippet(resp.Body))
	}
	var out MemoryLookupResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("memory.lookup decode: %w", err)
	}
	return &out, nil
}

// CommissionRequest is the subset of Minos's CommissionRequest Iris
// exposes as the `commission` Claude tool. ProjectID, RequestID, and
// Origin are filled in by Iris based on the inbound message.
type CommissionRequest struct {
	RepoURL    string `json:"repo_url"`
	Branch     string `json:"branch"`
	BaseBranch string `json:"base_branch,omitempty"`
	Summary    string `json:"summary"`
	// Origin tuple — captured from the inbound Hermes message so the
	// commissioned task records who asked.
	Surface       string `json:"surface"`
	SurfaceUserID string `json:"surface_user_id"`
	ThreadRef     string `json:"thread_ref"`
}

// Commission posts a new task. Returns the created task's id on success.
func (c *MinosClient) Commission(ctx context.Context, req CommissionRequest) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"task_type": "code",
		"brief":     map[string]string{"summary": req.Summary},
		"execution": map[string]string{
			"repo_url":    req.RepoURL,
			"branch":      req.Branch,
			"base_branch": req.BaseBranch,
		},
		"origin": map[string]string{
			"surface":    "hermes:" + req.Surface,
			"request_id": req.ThreadRef + ":iris:" + time.Now().UTC().Format("20060102T150405.000Z"),
			"requester":  req.SurfaceUserID,
		},
	})
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/tasks", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.AdminToken)
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("commission: %s: %s", resp.Status, readSnippet(resp.Body))
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("commission decode: %w", err)
	}
	id, _ := out["id"].(string)
	return id, nil
}

func (c *MinosClient) getTaskList(ctx context.Context, path string) ([]TaskSummary, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.IrisToken)
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s: %s", path, resp.Status, readSnippet(resp.Body))
	}
	var out []TaskSummary
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("%s decode: %w", path, err)
	}
	return out, nil
}

// readSnippet pulls up to 512 bytes of an error response body for
// inclusion in error messages.
func readSnippet(r io.Reader) string {
	buf := make([]byte, 512)
	n, _ := r.Read(buf)
	return string(buf[:n])
}
