package core_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zakros-hq/zakros/minos/core"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestLoadConfigValid(t *testing.T) {
	path := writeConfig(t, `{
  "listen_addr": ":8080",
  "database_url": "postgres://localhost/zakros",
  "bearer_secret_ref": "minos-bearer-secret",
  "admin_token_ref": "minos-admin-token",
  "github_webhook_secret_ref": "github-webhook-secret",
  "admin": {"surface": "discord", "surface_id": "123"},
  "project": {
    "id": "test",
    "backend": "claude-code",
    "plugin_image": "ghcr.io/example/zakros-claude-code:latest",
    "default_budget": {
      "max_tokens": 500000,
      "max_wall_clock_seconds": 3600,
      "warning_threshold_pct": 75,
      "escalation_threshold_pct": 90
    },
    "communication": {
      "thread_surface": "discord",
      "thread_ref": "",
      "hermes_url": "http://localhost/hermes",
      "argus_ingest_url": "http://localhost/argus",
      "ariadne_ingest_url": "http://localhost/ariadne"
    },
    "capabilities": {"injected_credentials": [], "mcp_endpoints": []}
  }
}`)
	cfg, err := core.LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Project.DefaultBaseBranch != "main" {
		t.Errorf("default base branch not applied: %q", cfg.Project.DefaultBaseBranch)
	}
	if cfg.Project.DefaultWorkspaceSize != "small" {
		t.Errorf("default workspace not applied: %q", cfg.Project.DefaultWorkspaceSize)
	}
}

func TestLoadConfigMissingFields(t *testing.T) {
	cases := []struct{ name, body string }{
		{"no listen", `{"bearer_secret_ref":"x","admin_token_ref":"y","github_webhook_secret_ref":"z","admin":{"surface":"s","surface_id":"i"},"project":{"id":"p","backend":"b","plugin_image":"i"}}`},
		{"no bearer", `{"listen_addr":":8080","admin_token_ref":"y","github_webhook_secret_ref":"z","admin":{"surface":"s","surface_id":"i"},"project":{"id":"p","backend":"b","plugin_image":"i"}}`},
		{"no webhook", `{"listen_addr":":8080","bearer_secret_ref":"x","admin_token_ref":"y","admin":{"surface":"s","surface_id":"i"},"project":{"id":"p","backend":"b","plugin_image":"i"}}`},
		{"no admin", `{"listen_addr":":8080","bearer_secret_ref":"x","admin_token_ref":"y","github_webhook_secret_ref":"z","project":{"id":"p","backend":"b","plugin_image":"i"}}`},
		{"no project.id", `{"listen_addr":":8080","bearer_secret_ref":"x","admin_token_ref":"y","github_webhook_secret_ref":"z","admin":{"surface":"s","surface_id":"i"},"project":{"backend":"b","plugin_image":"i"}}`},
		{"no plugin image", `{"listen_addr":":8080","bearer_secret_ref":"x","admin_token_ref":"y","github_webhook_secret_ref":"z","admin":{"surface":"s","surface_id":"i"},"project":{"id":"p","backend":"b"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfig(t, tc.body)
			if _, err := core.LoadConfig(path); err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}
