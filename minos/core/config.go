package core

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

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
	// Discord holds credentials for the Phase 1 Discord Hermes plugin.
	// Zero-value means the plugin is not wired.
	Discord DiscordConfig `json:"discord"`
	// Hibernation controls the awaiting-review sweep cadence and TTLs
	// (reminder → admin nudge, abandon → transition to failed). Defaults
	// are applied when fields are empty in the config file.
	Hibernation HibernationConfig `json:"hibernation"`
}

// AdminIdentity is the single hardcoded admin tuple checked at command
// intake per architecture.md §6 Command Intake and Pairing, Phase 1.
type AdminIdentity struct {
	Surface   string `json:"surface"`
	SurfaceID string `json:"surface_id"`
}

// DiscordConfig carries the credentials and channel IDs the Discord
// Hermes plugin needs. Empty values mean "no Discord plugin"; cmd/minos
// skips wiring Hermes in that case.
type DiscordConfig struct {
	// BotTokenRef is the secret provider reference whose value is the
	// Discord bot token.
	BotTokenRef string `json:"bot_token_ref"`
}

// HibernationConfig controls the awaiting-review sweep cadence + TTLs.
// Durations are strings (Go time.ParseDuration syntax, e.g. "24h"); empty
// strings disable the corresponding behavior.
type HibernationConfig struct {
	ReminderAfter string `json:"reminder_after"`
	AbandonAfter  string `json:"abandon_after"`
	SweepInterval string `json:"sweep_interval"`
}

// ProjectConfig holds the single-project defaults that feed envelope
// composition. Slice A fields only; Slice C adds context-assembly hints,
// Slice E adds Iris-specific overrides.
type ProjectConfig struct {
	ID                   string                 `json:"id"`
	Backend              string                 `json:"backend"`
	PluginImage          string                 `json:"plugin_image"`
	// AgentMentionHandle is the GitHub login/app-slug that, when @mentioned
	// in a PR comment (issue_comment event), triggers a respawn of the
	// bound task. Empty disables @mention respawning.
	AgentMentionHandle string `json:"agent_mention_handle"`
	DefaultWorkspaceSize envelope.WorkspaceSize `json:"default_workspace_size"`
	DefaultBaseBranch    string                 `json:"default_base_branch"`
	// ArgusSidecarImage, when non-empty, adds the Argus heartbeat sidecar
	// container to every dispatched pod.
	ArgusSidecarImage string          `json:"argus_sidecar_image"`
	DefaultBudget     envelope.Budget `json:"default_budget"`
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
	// Hibernation defaults per architecture.md §8 Review Activity and
	// Abandonment; operators can override any field in the config file.
	if c.Hibernation.ReminderAfter == "" {
		c.Hibernation.ReminderAfter = "24h"
	}
	if c.Hibernation.AbandonAfter == "" {
		c.Hibernation.AbandonAfter = "72h"
	}
	if c.Hibernation.SweepInterval == "" {
		c.Hibernation.SweepInterval = "5m"
	}
	if _, _, _, err := c.Hibernation.Durations(); err != nil {
		return fmt.Errorf("hibernation: %w", err)
	}
	return nil
}

// Durations parses the HibernationConfig's string fields. Empty strings
// return zero durations (which the sweeper treats as "disabled").
func (h HibernationConfig) Durations() (reminder, abandon, sweep time.Duration, err error) {
	if h.ReminderAfter != "" {
		reminder, err = time.ParseDuration(h.ReminderAfter)
		if err != nil {
			return 0, 0, 0, fmt.Errorf("reminder_after %q: %w", h.ReminderAfter, err)
		}
	}
	if h.AbandonAfter != "" {
		abandon, err = time.ParseDuration(h.AbandonAfter)
		if err != nil {
			return 0, 0, 0, fmt.Errorf("abandon_after %q: %w", h.AbandonAfter, err)
		}
	}
	if h.SweepInterval != "" {
		sweep, err = time.ParseDuration(h.SweepInterval)
		if err != nil {
			return 0, 0, 0, fmt.Errorf("sweep_interval %q: %w", h.SweepInterval, err)
		}
	}
	return reminder, abandon, sweep, nil
}
