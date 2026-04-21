// Package envelope defines the task envelope that Minos dispatches to worker
// backend pods. The schema is authoritative per architecture.md §8 Task
// Definition; Go types here mirror the JSON Schema in schemas/envelope.v1.json.
//
// Plugins validate against the JSON Schema directly; the Go types exist for
// Minos and other in-repo services to compose, dispatch, and inspect envelopes
// without reflection gymnastics.
package envelope

import "encoding/json"

// SchemaVersion is the current envelope schema revision. Bump on breaking
// changes; plugins compare against their declared minimum support.
const SchemaVersion = "1"

// TaskType enumerates the task types Minos dispatches. Per-type inputs and
// acceptance contracts live in type-specific helper packages.
type TaskType string

const (
	TaskTypeCode            TaskType = "code"
	TaskTypeInfra           TaskType = "infra"
	TaskTypeInferenceTuning TaskType = "inference-tuning"
	TaskTypeResearch        TaskType = "research"
	TaskTypeTest            TaskType = "test"
)

// WorkspaceSize selects the pod's ephemeral disk tier; sizes map to repository
// shapes per architecture.md §11 Workspace Sizing.
type WorkspaceSize string

const (
	WorkspaceSmall  WorkspaceSize = "small"
	WorkspaceMedium WorkspaceSize = "medium"
	WorkspaceLarge  WorkspaceSize = "large"
)

// Envelope is the full task payload a pod receives at spawn time.
type Envelope struct {
	SchemaVersion string   `json:"schema_version"`
	ID            string   `json:"id"`
	ParentID      *string  `json:"parent_id,omitempty"`
	ProjectID     string   `json:"project_id"`
	CreatedAt     string   `json:"created_at"`
	TaskType      TaskType `json:"task_type"`
	Backend       string   `json:"backend"`
	Origin        Origin   `json:"origin"`
	Brief         Brief    `json:"brief"`
	// Inputs and Acceptance are typed per TaskType; raw JSON at the envelope
	// level, parsed by the task-type-specific package.
	Inputs        json.RawMessage `json:"inputs"`
	Execution     Execution       `json:"execution"`
	Communication Communication   `json:"communication"`
	Capabilities  Capabilities    `json:"capabilities"`
	ContextRef    *string         `json:"context_ref,omitempty"`
	Budget        Budget          `json:"budget"`
	Acceptance    json.RawMessage `json:"acceptance"`
}

// Origin records how the task was commissioned. `surface` encodes the
// intake path (`hermes:<plugin>`, `github-webhook`, `github-mention`,
// `internal`); `requester` is the identity resolved at command intake.
type Origin struct {
	Surface   string `json:"surface"`
	RequestID string `json:"request_id"`
	Requester string `json:"requester"`
}

// Brief is the operator-facing task description.
type Brief struct {
	Summary string `json:"summary"`
	Detail  string `json:"detail"`
}

// Execution holds the workspace-provisioning inputs.
type Execution struct {
	RepoURL       string        `json:"repo_url"`
	Branch        string        `json:"branch"`
	BaseBranch    string        `json:"base_branch"`
	WorkspaceSize WorkspaceSize `json:"workspace_size"`
}

// Communication holds the endpoints the thread and Argus sidecars need to
// reach back to the Minos-VM brokers.
type Communication struct {
	ThreadSurface    string `json:"thread_surface"`
	ThreadRef        string `json:"thread_ref"`
	HermesURL        string `json:"hermes_url"`
	ArgusIngestURL   string `json:"argus_ingest_url"`
	AriadneIngestURL string `json:"ariadne_ingest_url"`
}

// Capabilities gates what the pod can do: direct-inject credentials go to
// the worker backend's environment; mcp_endpoints name the brokers the pod
// may call under the accompanying JWT (Phase 2) or bearer token (Phase 1).
type Capabilities struct {
	InjectedCredentials []InjectedCredential `json:"injected_credentials"`
	McpEndpoints        []McpEndpoint        `json:"mcp_endpoints"`
	// McpAuthToken is the bearer (Phase 1) or Minos-minted JWT (Phase 2) the
	// pod presents to every MCP broker it calls. Scopes on the token must
	// match the scopes array on each endpoint.
	McpAuthToken string `json:"mcp_auth_token"`
}

// InjectedCredential names a secret provider reference and the environment
// variable the pod should see it under.
type InjectedCredential struct {
	EnvVar         string `json:"env_var"`
	CredentialsRef string `json:"credentials_ref"`
}

// McpEndpoint names a broker the pod may reach and the scopes authorized for
// this task. Scopes are documentation-and-selfcheck; the JWT (or bearer)
// remains the authoritative gate.
type McpEndpoint struct {
	Name   string   `json:"name"`
	URL    string   `json:"url"`
	Scopes []string `json:"scopes"`
}

// Budget caps token and wall-clock spend; thresholds trigger Argus warnings
// and escalations.
type Budget struct {
	MaxTokens              int `json:"max_tokens"`
	MaxWallClockSeconds    int `json:"max_wall_clock_seconds"`
	WarningThresholdPct    int `json:"warning_threshold_pct"`
	EscalationThresholdPct int `json:"escalation_threshold_pct"`
}
