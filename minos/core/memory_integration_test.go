package core_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/GoodOlClint/daedalus/cerberus/core/replay"
	"github.com/GoodOlClint/daedalus/minos/core"
	"github.com/GoodOlClint/daedalus/minos/dispatch/fakedispatch"
	"github.com/GoodOlClint/daedalus/minos/storage/memstore"
	mnemocore "github.com/GoodOlClint/daedalus/mnemosyne/core"
	mnemomem "github.com/GoodOlClint/daedalus/mnemosyne/memstore"
	"github.com/GoodOlClint/daedalus/pkg/audit"
	"github.com/GoodOlClint/daedalus/pkg/envelope"
)

// newTestServerWithMnemosyne builds a rig with a memstore-backed mnemosyne
// wired in so context injection + memory persistence are exercised.
func newTestServerWithMnemosyne(t *testing.T) (kit testServerKit, mnemo *mnemomem.Store) {
	t.Helper()
	bearerSecret := []byte("bearer-secret-for-tests")
	webhookSecret := []byte("webhook-secret-for-tests")
	prov := &staticProvider{refs: map[string][]byte{
		"minos-bearer-secret":   bearerSecret,
		"minos-admin-token":     []byte("admin-token"),
		"minos-iris-token":      []byte("iris-token"),
		"github-app-token":      []byte("ghs_injected"),
		"github-webhook-secret": webhookSecret,
	}}
	cfg := core.Config{
		ListenAddr:             ":0",
		BearerSecretRef:        "minos-bearer-secret",
		AdminTokenRef:          "minos-admin-token",
		IrisTokenRef:           "minos-iris-token",
		GithubWebhookSecretRef: "github-webhook-secret",
		Admin:                  core.AdminIdentity{Surface: "discord", SurfaceID: "admin-id"},
		Project: core.ProjectConfig{
			ID:                   "mnemo-project",
			Backend:              "claude-code",
			PluginImage:          "ghcr.io/example/plugin:latest",
			DefaultWorkspaceSize: envelope.WorkspaceSmall,
			DefaultBaseBranch:    "main",
			DefaultBudget: envelope.Budget{
				MaxTokens: 1000, MaxWallClockSeconds: 60,
				WarningThresholdPct: 75, EscalationThresholdPct: 90,
			},
			Communication: envelope.Communication{
				HermesURL:        "http://x",
				ArgusIngestURL:   "http://x",
				AriadneIngestURL: "http://x",
			},
			Capabilities: core.CapabilitiesDefaults{
				InjectedCredentials: []envelope.InjectedCredential{
					{EnvVar: "GITHUB_TOKEN", CredentialsRef: "github-app-token"},
				},
			},
		},
	}
	store := memstore.New(nil)
	disp := fakedispatch.New()
	rs := replay.NewMemStore(0)
	mnemo = mnemomem.New()
	srv, err := core.New(cfg, prov, store, disp, audit.NewWriterEmitter("t", discardWriter{}),
		core.WithReplayStore(rs),
		core.WithMnemosyne(mnemo),
	)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	return testServerKit{
		server: srv, store: store, dispatcher: disp,
		bearerSecret: bearerSecret, webhookSecret: webhookSecret,
	}, mnemo
}

func TestReportMemoryPersistsSanitized(t *testing.T) {
	kit, mnemo := newTestServerWithMnemosyne(t)

	task, err := kit.server.Commission(context.Background(), core.CommissionRequest{
		TaskType:  envelope.TaskTypeCode,
		Brief:     envelope.Brief{Summary: "x"},
		Execution: core.ExecutionRequest{RepoURL: "https://example.com/r", Branch: "fix/a"},
	})
	if err != nil {
		t.Fatalf("commission: %v", err)
	}
	podToken := task.Envelope.Capabilities.McpAuthToken

	ts := httptest.NewServer(core.TestingHTTPHandler(kit.server))
	defer ts.Close()

	// Include the injected credential value in the body to prove
	// sanitization strips it out.
	body := map[string]any{
		"outcome": "completed",
		"summary": "opened PR https://github.com/x/y/pull/1",
		"body": map[string]any{
			"log": "authorized with token ghs_injected then pushed",
		},
	}
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(context.Background(), "POST", ts.URL+"/tasks/"+task.ID.String()+"/memory", bytes.NewReader(buf))
	req.Header.Set("Authorization", "Bearer "+podToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	runs, err := mnemo.GetRunsForTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("get runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run stored, got %d", len(runs))
	}
	got := runs[0]
	if got.Outcome != mnemocore.OutcomeCompleted {
		t.Errorf("outcome: %s", got.Outcome)
	}
	if strings.Contains(string(got.Body), "ghs_injected") {
		t.Errorf("credential not sanitized from stored body: %s", got.Body)
	}
	if !strings.Contains(string(got.Body), "<redacted>") {
		t.Errorf("expected <redacted> marker in stored body: %s", got.Body)
	}
}

func TestCommissionInjectsPriorContext(t *testing.T) {
	kit, mnemo := newTestServerWithMnemosyne(t)
	ctx := context.Background()

	// Seed a prior run for the same project.
	if err := mnemo.StoreRun(ctx, &mnemocore.RunRecord{
		TaskID:    uuid.New(),
		RunID:     uuid.New(),
		ProjectID: "mnemo-project",
		TaskType:  "code",
		Outcome:   mnemocore.OutcomeCompleted,
		Summary:   "fixed the widget leak",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	task, err := kit.server.Commission(ctx, core.CommissionRequest{
		TaskType:  envelope.TaskTypeCode,
		Brief:     envelope.Brief{Summary: "next task"},
		Execution: core.ExecutionRequest{RepoURL: "https://example.com/r", Branch: "next/a"},
	})
	if err != nil {
		t.Fatalf("commission: %v", err)
	}
	if task.Envelope.ContextRef == nil || *task.Envelope.ContextRef == "" {
		t.Errorf("expected context_ref populated from prior run, got %v", task.Envelope.ContextRef)
	}
}

func TestCommissionNoContextWhenNoPriorRuns(t *testing.T) {
	kit, _ := newTestServerWithMnemosyne(t)
	task, err := kit.server.Commission(context.Background(), core.CommissionRequest{
		TaskType:  envelope.TaskTypeCode,
		Brief:     envelope.Brief{Summary: "first ever"},
		Execution: core.ExecutionRequest{RepoURL: "https://example.com/r", Branch: "first/a"},
	})
	if err != nil {
		t.Fatalf("commission: %v", err)
	}
	if task.Envelope.ContextRef != nil {
		t.Errorf("expected nil context_ref with no prior runs, got %q", *task.Envelope.ContextRef)
	}
}
