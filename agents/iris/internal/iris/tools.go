package iris

import (
	"context"
	"encoding/json"
	"fmt"
)

// commissionContext carries the originating Hermes message so the
// `commission` tool can stamp origin without the model needing to pass
// it. Set per inbound message; consumed by the tool handler.
type commissionContext struct {
	Surface       string
	SurfaceUserID string
	ThreadRef     string
}

// ToolSet groups Iris's tools and the dependencies they need. One
// instance per Iris pod; per-message commissionContext flows through
// Run() invocations rather than being held on the struct.
type ToolSet struct {
	Minos *MinosClient

	// ProjectID is the Phase 1 single-project id Iris stamps on
	// memory.lookup calls. Phase 2 Slice G replaces this with project
	// resolution from the conversation's surface/thread mapping.
	ProjectID string

	// DefaultBranchPrefix is what `commission` uses when the model
	// doesn't supply an explicit branch name. Defaults to "iris/".
	DefaultBranchPrefix string

	// DefaultRepoURL is used when the model omits repo_url. Phase 1
	// single-project, single-repo posture lets us default; Phase 2
	// expects the model to always pick a repo.
	DefaultRepoURL string
}

// Definitions returns the tool list passed to Anthropic. JSON Schema
// shapes are deliberately small — each tool has the minimum fields the
// Slice 0 close-out needs.
func (ts *ToolSet) Definitions() []Tool {
	return []Tool{
		{
			Name:        "query_state",
			Description: "List Zakros tasks by state. Use this to answer 'what's running?', 'what's queued?', or 'what just finished?'.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"scope": map[string]any{
						"type":        "string",
						"description": "Which task set to return. 'active' = queued + running + awaiting-review (default); 'queue' = queued only; 'recent' = recently completed/failed.",
						"enum":        []string{"active", "queue", "recent"},
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Max tasks to return. Defaults to 20.",
					},
				},
			},
		},
		{
			Name:        "commission",
			Description: "Commission a new Zakros task. Use this when the operator asks Iris to start work on something.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"summary": map[string]any{
						"type":        "string",
						"description": "Short brief describing the work. Required.",
					},
					"branch": map[string]any{
						"type":        "string",
						"description": "Feature branch name to use. Defaults to a generated 'iris/<slug>' branch when omitted.",
					},
					"repo_url": map[string]any{
						"type":        "string",
						"description": "Repository URL. Defaults to the project's configured repo when omitted.",
					},
					"base_branch": map[string]any{
						"type":        "string",
						"description": "Base branch the feature branch is cut from. Defaults to the project's configured base.",
					},
				},
				"required": []string{"summary"},
			},
		},
	}
}

// Run dispatches one tool_use block. Returns the tool_result content
// the model receives back. Errors that the model should see (bad input,
// upstream non-2xx) are wrapped so the loop can encode them as
// is_error: true tool_results rather than aborting.
func (ts *ToolSet) Run(ctx context.Context, name string, input map[string]any, cc commissionContext) (string, error) {
	switch name {
	case "query_state":
		return ts.runQueryState(ctx, input)
	case "commission":
		return ts.runCommission(ctx, input, cc)
	default:
		return "", fmt.Errorf("%w: unknown tool %q", ErrToolError, name)
	}
}

func (ts *ToolSet) runQueryState(ctx context.Context, input map[string]any) (string, error) {
	scope, _ := input["scope"].(string)
	if scope == "" {
		scope = "active"
	}
	limit := 20
	if v, ok := input["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}

	var (
		tasks []TaskSummary
		err   error
	)
	switch scope {
	case "queue":
		tasks, err = ts.Minos.StateQueue(ctx)
	case "recent":
		tasks, err = ts.Minos.StateRecent(ctx, limit)
	default: // "active"
		tasks, err = ts.Minos.StateTasks(ctx,
			"queued,running,awaiting-review", limit)
	}
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrToolError, err)
	}
	out, err := json.Marshal(map[string]any{
		"scope": scope,
		"count": len(tasks),
		"tasks": tasks,
	})
	if err != nil {
		return "", fmt.Errorf("%w: encode: %v", ErrToolError, err)
	}
	return string(out), nil
}

func (ts *ToolSet) runCommission(ctx context.Context, input map[string]any, cc commissionContext) (string, error) {
	summary, _ := input["summary"].(string)
	if summary == "" {
		return "", fmt.Errorf("%w: summary required", ErrToolError)
	}
	branch, _ := input["branch"].(string)
	if branch == "" {
		branch = ts.DefaultBranchPrefix + slugify(summary)
	}
	repoURL, _ := input["repo_url"].(string)
	if repoURL == "" {
		repoURL = ts.DefaultRepoURL
	}
	if repoURL == "" {
		return "", fmt.Errorf("%w: repo_url required (no project default configured)", ErrToolError)
	}
	baseBranch, _ := input["base_branch"].(string)

	id, err := ts.Minos.Commission(ctx, CommissionRequest{
		RepoURL:       repoURL,
		Branch:        branch,
		BaseBranch:    baseBranch,
		Summary:       summary,
		Surface:       cc.Surface,
		SurfaceUserID: cc.SurfaceUserID,
		ThreadRef:     cc.ThreadRef,
	})
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrToolError, err)
	}
	out, err := json.Marshal(map[string]any{
		"task_id":  id,
		"branch":   branch,
		"repo_url": repoURL,
		"summary":  summary,
	})
	if err != nil {
		return "", fmt.Errorf("%w: encode: %v", ErrToolError, err)
	}
	return string(out), nil
}

// slugify produces a short branch slug from a summary. Lowercase,
// alnum + hyphen, capped at 32 chars. Not bulletproof — Phase 1 is
// "good enough for an admin who can rename if needed."
func slugify(s string) string {
	const max = 32
	out := make([]byte, 0, max)
	prevHyphen := true
	for i := 0; i < len(s) && len(out) < max; i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			out = append(out, c)
			prevHyphen = false
		case c >= 'A' && c <= 'Z':
			out = append(out, c+('a'-'A'))
			prevHyphen = false
		default:
			if !prevHyphen {
				out = append(out, '-')
				prevHyphen = true
			}
		}
	}
	for len(out) > 0 && out[len(out)-1] == '-' {
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		return "task"
	}
	return string(out)
}
