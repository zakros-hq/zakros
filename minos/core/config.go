package core

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/GoodOlClint/daedalus/pkg/envelope"
)

// Config is the Minos daemon configuration. Phase 1 is single-project
// single-admin per roadmap.md §Phase 1 Scope Anchors; ProjectConfig and
// AdminIdentity are scalars accordingly. Phase 2 replaces Project with a
// registry and AdminIdentity with the identity registry.
type Config struct {
	ListenAddr             string        `json:"listen_addr"`
	DatabaseURL            string        `json:"database_url"`
	BearerSecretRef        string        `json:"bearer_secret_ref"`
	AdminTokenRef          string        `json:"admin_token_ref"`
	GithubWebhookSecretRef string        `json:"github_webhook_secret_ref"`
	// MinosPodURL is the Minos API URL as seen from inside a Labyrinth
	// pod. Injected into the pod as DAEDALUS_MINOS_URL so the entrypoint
	// can POST /tasks/{id}/pr after opening the PR.
	MinosPodURL string        `json:"minos_pod_url"`
	Admin       AdminIdentity `json:"admin"`
	Project     ProjectConfig `json:"project"`
}

// AdminIdentity is the single hardcoded admin tuple checked at command
// intake per architecture.md §6 Command Intake and Pairing, Phase 1.
type AdminIdentity struct {
	Surface   string `json:"surface"`
	SurfaceID string `json:"surface_id"`
}

// ProjectConfig holds the single-project defaults that feed envelope
// composition. Slice A fields only; Slice C adds context-assembly hints,
// Slice E adds Iris-specific overrides.
type ProjectConfig struct {
	ID                   string                 `json:"id"`
	Backend              string                 `json:"backend"`
	PluginImage          string                 `json:"plugin_image"`
	DefaultWorkspaceSize envelope.WorkspaceSize `json:"default_workspace_size"`
	DefaultBaseBranch    string                 `json:"default_base_branch"`
	DefaultBudget        envelope.Budget        `json:"default_budget"`
	Communication        envelope.Communication `json:"communication"`
	// ThreadParent is the surface-specific container where new task
	// threads get created (Discord channel ID, Slack channel ID, etc.).
	// Required when a Hermes plugin is wired in and CreateThread is
	// expected to succeed; optional otherwise.
	ThreadParent string               `json:"thread_parent"`
	Capabilities CapabilitiesDefaults `json:"capabilities"`
}

// CapabilitiesDefaults are the project-wide capability defaults composed
// into every task envelope for this project.
type CapabilitiesDefaults struct {
	InjectedCredentials []envelope.InjectedCredential `json:"injected_credentials"`
	McpEndpoints        []envelope.McpEndpoint        `json:"mcp_endpoints"`
}

// LoadConfig reads a JSON config file from path.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load config %s: %w", path, err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	if err := validateConfig(&c); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return &c, nil
}

func validateConfig(c *Config) error {
	if c.ListenAddr == "" {
		return fmt.Errorf("listen_addr required")
	}
	if c.BearerSecretRef == "" {
		return fmt.Errorf("bearer_secret_ref required")
	}
	if c.AdminTokenRef == "" {
		return fmt.Errorf("admin_token_ref required")
	}
	if c.GithubWebhookSecretRef == "" {
		return fmt.Errorf("github_webhook_secret_ref required")
	}
	if c.Admin.Surface == "" || c.Admin.SurfaceID == "" {
		return fmt.Errorf("admin identity (surface, surface_id) required")
	}
	if c.Project.ID == "" {
		return fmt.Errorf("project.id required")
	}
	if c.Project.Backend == "" {
		return fmt.Errorf("project.backend required")
	}
	if c.Project.PluginImage == "" {
		return fmt.Errorf("project.plugin_image required")
	}
	if c.Project.DefaultWorkspaceSize == "" {
		c.Project.DefaultWorkspaceSize = envelope.WorkspaceSmall
	}
	if c.Project.DefaultBaseBranch == "" {
		c.Project.DefaultBaseBranch = "main"
	}
	return nil
}
