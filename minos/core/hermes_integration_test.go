package core_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	hermescore "github.com/GoodOlClint/daedalus/hermes/core"
	"github.com/GoodOlClint/daedalus/hermes/plugins/fakeplugin"
	"github.com/GoodOlClint/daedalus/minos/core"
	"github.com/GoodOlClint/daedalus/minos/dispatch/fakedispatch"
	"github.com/GoodOlClint/daedalus/minos/storage"
	"github.com/GoodOlClint/daedalus/minos/storage/memstore"
	"github.com/GoodOlClint/daedalus/pkg/audit"
	"github.com/GoodOlClint/daedalus/pkg/envelope"

	"github.com/GoodOlClint/daedalus/cerberus/core/replay"
)

// newTestServerWithHermes builds a server rig with a fake Hermes plugin
// registered for the "discord" surface.
func newTestServerWithHermes(t *testing.T) (kit testServerKit, plugin *fakeplugin.Plugin) {
	t.Helper()
	bearerSecret := []byte("bearer-secret-for-tests")
	webhookSecret := []byte("webhook-secret-for-tests")
	prov := &staticProvider{refs: map[string][]byte{
		"minos-bearer-secret":   bearerSecret,
		"minos-admin-token":     []byte("admin-token"),
		"github-app-token":      []byte("ghs_injected"),
		"github-webhook-secret": webhookSecret,
	}}
	cfg := core.Config{
		ListenAddr:             ":0",
		BearerSecretRef:        "minos-bearer-secret",
		AdminTokenRef:          "minos-admin-token",
		GithubWebhookSecretRef: "github-webhook-secret",
		Admin:                  core.AdminIdentity{Surface: "discord", SurfaceID: "admin-id"},
		Project: core.ProjectConfig{
			ID:                   "test-project",
			Backend:              "claude-code",
			PluginImage:          "ghcr.io/example/plugin:latest",
			AgentMentionHandle:   "daedalus-bot",
			DefaultWorkspaceSize: envelope.WorkspaceSmall,
			DefaultBaseBranch:    "main",
			ThreadParent:         "channel-ops",
			DefaultBudget: envelope.Budget{
				MaxTokens: 1000, MaxWallClockSeconds: 60,
				WarningThresholdPct: 75, EscalationThresholdPct: 90,
			},
			Communication: envelope.Communication{
				ThreadSurface:    "discord",
				HermesURL:        "http://minos:8081/hermes",
				ArgusIngestURL:   "http://minos:8081/argus",
				AriadneIngestURL: "http://ariadne:8082/ingest",
			},
			Capabilities: core.CapabilitiesDefaults{
				InjectedCredentials: []envelope.InjectedCredential{
					{EnvVar: "GITHUB_TOKEN", CredentialsRef: "github-app-token"},
				},
				McpEndpoints: []envelope.McpEndpoint{
					{Name: "github", URL: "http://minos:8081/mcp/github", Scopes: []string{"pr.create"}},
				},
			},
		},
		Hibernation: core.HibernationConfig{
			ReminderAfter: "24h",
			AbandonAfter:  "72h",
			SweepInterval: "5m",
		},
	}

	plug := fakeplugin.New("discord")
	broker := hermescore.New()
	if err := broker.RegisterPlugin(plug); err != nil {
		t.Fatalf("register plugin: %v", err)
	}
	if err := broker.Start(context.Background()); err != nil {
		t.Fatalf("start broker: %v", err)
	}

	store := memstore.New(nil)
	disp := fakedispatch.New()
	rs := replay.NewMemStore(0)
	srv, err := core.New(cfg, prov, store, disp, audit.NewWriterEmitter("t", discardWriter{}),
		core.WithReplayStore(rs),
		core.WithHermes(broker),
	)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return testServerKit{
		server: srv, store: store, dispatcher: disp,
		bearerSecret: bearerSecret, webhookSecret: webhookSecret,
	}, plug
}

func TestCommissionCreatesThread(t *testing.T) {
	kit, plug := newTestServerWithHermes(t)
	task, err := kit.server.Commission(context.Background(), core.CommissionRequest{
		TaskType:  envelope.TaskTypeCode,
		Brief:     envelope.Brief{Summary: "fix the widget"},
		Execution: core.ExecutionRequest{RepoURL: "https://example.com/r", Branch: "fix/a"},
		Origin:    envelope.Origin{Surface: "internal", RequestID: "t-1", Requester: "admin"},
	})
	if err != nil {
		t.Fatalf("commission: %v", err)
	}
	threads := plug.Threads()
	if len(threads) != 1 {
		t.Fatalf("expected 1 thread created, got %d", len(threads))
	}
	thread := threads[0]
	if thread.Parent != "channel-ops" {
		t.Errorf("parent: %q", thread.Parent)
	}
	if task.Envelope.Communication.ThreadRef != thread.Ref {
		t.Errorf("envelope thread_ref %q does not match created %q", task.Envelope.Communication.ThreadRef, thread.Ref)
	}
}

func TestWebhookMergedPostsSummary(t *testing.T) {
	kit, plug := newTestServerWithHermes(t)
	task, podToken := commissionAndGrabToken(t, kit)

	ts := httptest.NewServer(core.TestingHTTPHandler(kit.server))
	defer ts.Close()

	// Bind PR.
	prURL := "https://github.com/example/r/pull/1"
	prBody, _ := json.Marshal(map[string]string{"pr_url": prURL})
	prReq, _ := http.NewRequestWithContext(context.Background(), "POST", ts.URL+"/tasks/"+task.ID.String()+"/pr", bytes.NewReader(prBody))
	prReq.Header.Set("Authorization", "Bearer "+podToken)
	prReq.Header.Set("Content-Type", "application/json")
	prResp, _ := http.DefaultClient.Do(prReq)
	prResp.Body.Close()

	// Deliver webhook.
	payload := []byte(`{"action":"closed","pull_request":{"html_url":"` + prURL + `","merged":true}}`)
	req, _ := http.NewRequestWithContext(context.Background(), "POST", ts.URL+"/webhooks/github", bytes.NewReader(payload))
	req.Header.Set("X-Hub-Signature-256", signGithub(kit.webhookSecret, payload))
	req.Header.Set("X-GitHub-Delivery", "delivery-1")
	req.Header.Set("X-GitHub-Event", "pull_request")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()

	stored, _ := kit.store.GetTask(context.Background(), task.ID)
	if stored.State != storage.StateCompleted {
		t.Errorf("expected completed, got %s", stored.State)
	}

	threads := plug.Threads()
	if len(threads) != 1 {
		t.Fatalf("expected 1 thread, got %d", len(threads))
	}
	var summaries int
	for _, post := range threads[0].Posts {
		if post.Kind == hermescore.KindSummary {
			summaries++
		}
	}
	if summaries != 1 {
		t.Errorf("expected 1 summary post, got %d", summaries)
	}
}

func TestCommissionSkipsThreadWhenNoPlugin(t *testing.T) {
	// Kit without Hermes — ensures the "no plugin" path stays working for
	// CLI-driven Slice A commissioning.
	kit := newTestServer(t)
	task, err := kit.server.Commission(context.Background(), core.CommissionRequest{
		TaskType:  envelope.TaskTypeCode,
		Brief:     envelope.Brief{Summary: "x"},
		Execution: core.ExecutionRequest{RepoURL: "https://example.com/r", Branch: "fix/a"},
	})
	if err != nil {
		t.Fatalf("commission: %v", err)
	}
	if task.Envelope.Communication.ThreadRef != "" {
		t.Errorf("expected empty thread_ref, got %q", task.Envelope.Communication.ThreadRef)
	}
}
