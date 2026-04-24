package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/GoodOlClint/daedalus/cerberus/core/replay"
	"github.com/GoodOlClint/daedalus/minos/core"
	"github.com/GoodOlClint/daedalus/minos/dispatch"
	"github.com/GoodOlClint/daedalus/minos/dispatch/fakedispatch"
	"github.com/GoodOlClint/daedalus/minos/storage"
	"github.com/GoodOlClint/daedalus/minos/storage/memstore"
	"github.com/GoodOlClint/daedalus/pkg/audit"
	"github.com/GoodOlClint/daedalus/pkg/envelope"
	"github.com/GoodOlClint/daedalus/pkg/jwt"
	"github.com/GoodOlClint/daedalus/pkg/provider"
)

// staticProvider is a test double that resolves a single credential by ref.
type staticProvider struct {
	refs map[string][]byte
}

func (p *staticProvider) Resolve(_ context.Context, ref string) (*provider.Value, error) {
	v, ok := p.refs[ref]
	if !ok {
		return nil, provider.ErrNotFound
	}
	return &provider.Value{Ref: ref, Data: v}, nil
}

func (p *staticProvider) Rotate(context.Context, string) error { return nil }
func (p *staticProvider) Revoke(context.Context, string) error { return nil }
func (p *staticProvider) AuditList(context.Context, string) ([]provider.AuditEntry, error) {
	return nil, nil
}

// testServerKit bundles the commonly-accessed pieces of the test rig so
// individual tests can reach into the dispatcher or store without the
// helper signature growing unbounded.
type testServerKit struct {
	server        *core.Server
	store         *memstore.Store
	dispatcher    *fakedispatch.Dispatcher
	bearerSecret  []byte
	webhookSecret []byte
}

func newTestServer(t *testing.T) testServerKit {
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
		Admin: core.AdminIdentity{
			Surface:   "discord",
			SurfaceID: "admin-id",
		},
		Project: core.ProjectConfig{
			ID:                   "test-project",
			Backend:              "claude-code",
			PluginImage:          "ghcr.io/example/daedalus-claude-code:latest",
			DefaultWorkspaceSize: envelope.WorkspaceSmall,
			DefaultBaseBranch:    "main",
			DefaultBudget: envelope.Budget{
				MaxTokens:              100000,
				MaxWallClockSeconds:    600,
				WarningThresholdPct:    75,
				EscalationThresholdPct: 90,
			},
			Communication: envelope.Communication{
				ThreadSurface:    "discord",
				ThreadRef:        "",
				HermesURL:        "http://minos:8081/hermes",
				ArgusIngestURL:   "http://minos:8081/argus",
				AriadneIngestURL: "http://ariadne:8082/ingest",
			},
			Capabilities: core.CapabilitiesDefaults{
				InjectedCredentials: []envelope.InjectedCredential{
					{EnvVar: "GITHUB_TOKEN", CredentialsRef: "github-app-token"},
				},
				McpEndpoints: []envelope.McpEndpoint{
					{Name: "thread", URL: "http://localhost/thread", Scopes: []string{"post_status"}},
					{Name: "github", URL: "http://minos:8081/mcp/github", Scopes: []string{"pr.create", "pr.comment"}},
				},
			},
		},
	}
	store := memstore.New(nil)
	disp := fakedispatch.New()
	rs := replay.NewMemStore(0)
	srv, err := core.New(cfg, prov, store, disp, audit.NewWriterEmitter("minos-test", discardWriter{}),
		core.WithReplayStore(rs),
	)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return testServerKit{
		server:        srv,
		store:         store,
		dispatcher:    disp,
		bearerSecret:  bearerSecret,
		webhookSecret: webhookSecret,
	}
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestCommissionValidates(t *testing.T) {
	kit := newTestServer(t)
	srv := kit.server
	ctx := context.Background()

	cases := []struct {
		name string
		req  core.CommissionRequest
	}{
		{"missing task_type", core.CommissionRequest{
			Brief:     envelope.Brief{Summary: "x"},
			Execution: core.ExecutionRequest{RepoURL: "https://example.com", Branch: "f/x"},
		}},
		{"missing summary", core.CommissionRequest{
			TaskType:  envelope.TaskTypeCode,
			Execution: core.ExecutionRequest{RepoURL: "https://example.com", Branch: "f/x"},
		}},
		{"missing repo", core.CommissionRequest{
			TaskType:  envelope.TaskTypeCode,
			Brief:     envelope.Brief{Summary: "x"},
			Execution: core.ExecutionRequest{Branch: "f/x"},
		}},
		{"unsupported task_type", core.CommissionRequest{
			TaskType:  envelope.TaskTypeResearch,
			Brief:     envelope.Brief{Summary: "x"},
			Execution: core.ExecutionRequest{RepoURL: "https://example.com", Branch: "f/x"},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := srv.Commission(ctx, tc.req); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

func TestCommissionComposesEnvelope(t *testing.T) {
	kit := newTestServer(t)
	srv := kit.server
	bearerSecret := kit.bearerSecret
	ctx := context.Background()

	req := core.CommissionRequest{
		TaskType: envelope.TaskTypeCode,
		Brief:    envelope.Brief{Summary: "fix bug 123", Detail: "the widget leaks on teardown"},
		Execution: core.ExecutionRequest{
			RepoURL: "https://github.com/example/widget",
			Branch:  "fix/widget-teardown",
		},
		Origin: envelope.Origin{
			Surface:   "internal",
			RequestID: "cli-1",
			Requester: "admin",
		},
	}
	task, err := srv.Commission(ctx, req)
	if err != nil {
		t.Fatalf("commission: %v", err)
	}
	env := task.Envelope
	if env == nil {
		t.Fatal("envelope nil")
	}
	if env.SchemaVersion != envelope.SchemaVersion {
		t.Errorf("schema version: %s", env.SchemaVersion)
	}
	if env.Execution.BaseBranch != "main" {
		t.Errorf("default base branch not applied: %s", env.Execution.BaseBranch)
	}
	if env.Execution.WorkspaceSize != envelope.WorkspaceSmall {
		t.Errorf("default workspace size not applied: %s", env.Execution.WorkspaceSize)
	}
	if env.Budget.MaxTokens != 100000 {
		t.Errorf("default budget not applied: %+v", env.Budget)
	}
	if env.Capabilities.McpAuthToken == "" {
		t.Fatal("mcp_auth_token not minted")
	}

	// Verify the bearer round-trips.
	claims, err := jwt.VerifyBearer(bearerSecret, env.Capabilities.McpAuthToken)
	if err != nil {
		t.Fatalf("verify minted bearer: %v", err)
	}
	if claims.Subject != "task:"+task.ID.String() {
		t.Errorf("unexpected subject: %s", claims.Subject)
	}
	if !claims.HasScope("github", "pr.create") {
		t.Errorf("github:pr.create scope missing from minted claims: %+v", claims.McpScopes)
	}
	if !claims.HasScope("thread", "post_status") {
		t.Errorf("thread:post_status scope missing from minted claims: %+v", claims.McpScopes)
	}
}

func TestCommissionInputsDefault(t *testing.T) {
	kit := newTestServer(t)
	srv := kit.server
	req := core.CommissionRequest{
		TaskType:  envelope.TaskTypeCode,
		Brief:     envelope.Brief{Summary: "x"},
		Execution: core.ExecutionRequest{RepoURL: "https://example.com", Branch: "f/x"},
	}
	task, err := srv.Commission(context.Background(), req)
	if err != nil {
		t.Fatalf("commission: %v", err)
	}
	var inputs map[string]any
	if err := json.Unmarshal(task.Envelope.Inputs, &inputs); err != nil {
		t.Fatalf("inputs not valid JSON: %v", err)
	}
}

func TestCommissionSpawnsPodAndTransitionsRunning(t *testing.T) {
	kit := newTestServer(t)
	req := core.CommissionRequest{
		TaskType:  envelope.TaskTypeCode,
		Brief:     envelope.Brief{Summary: "fix it"},
		Execution: core.ExecutionRequest{RepoURL: "https://example.com/repo", Branch: "fix/a"},
		Origin:    envelope.Origin{Surface: "internal", RequestID: "t-1", Requester: "admin"},
	}
	task, err := kit.server.Commission(context.Background(), req)
	if err != nil {
		t.Fatalf("commission: %v", err)
	}
	if task.State != storage.StateRunning {
		t.Errorf("expected running state, got %s", task.State)
	}
	if task.PodName == nil || *task.PodName == "" {
		t.Fatal("pod name not set")
	}
	if task.RunID == nil {
		t.Fatal("run id not set")
	}

	pods := kit.dispatcher.Pods()
	if len(pods) != 1 {
		t.Fatalf("expected 1 pod in dispatcher, got %d", len(pods))
	}
	pod := pods[0]
	if pod.Spec.SecretEnv["GITHUB_TOKEN"] != "ghs_injected" {
		t.Errorf("injected credential not resolved into spec: %q", pod.Spec.SecretEnv["GITHUB_TOKEN"])
	}
	if pod.Spec.Labels["daedalus.project/task-id"] != task.ID.String() {
		t.Errorf("task-id label missing from spec")
	}

	// Store agrees with what Commission returned.
	stored, err := kit.store.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.State != storage.StateRunning {
		t.Errorf("store state: %s", stored.State)
	}
}

func TestCommissionDispatchFailureMarksFailed(t *testing.T) {
	kit := newTestServer(t)
	kit.dispatcher.SpawnError = errors.New("k3s pod quota exceeded")

	req := core.CommissionRequest{
		TaskType:  envelope.TaskTypeCode,
		Brief:     envelope.Brief{Summary: "fix it"},
		Execution: core.ExecutionRequest{RepoURL: "https://example.com/repo", Branch: "fix/a"},
		Origin:    envelope.Origin{Surface: "internal", RequestID: "t-1", Requester: "admin"},
	}
	task, err := kit.server.Commission(context.Background(), req)
	if err == nil {
		t.Fatal("expected dispatch failure to propagate")
	}
	if task == nil {
		t.Fatal("task should be returned even on dispatch failure so operator can see state")
	}
	stored, err := kit.store.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.State != storage.StateFailed {
		t.Errorf("expected failed state, got %s", stored.State)
	}
	if len(kit.dispatcher.Pods()) != 0 {
		t.Errorf("expected no pods after spawn failure, got %d", len(kit.dispatcher.Pods()))
	}
}

func TestCommissionUnresolvableCredentialFails(t *testing.T) {
	kit := newTestServer(t)
	// Break the provider by replacing the server's provider lookup for the
	// GITHUB_TOKEN ref — simulate an operator forgetting to seed the secret.
	// Easiest way: use a fresh server with a provider that lacks the entry.
	bearerSecret := []byte("bearer-secret-for-tests")
	prov := &staticProvider{refs: map[string][]byte{
		"minos-bearer-secret": bearerSecret,
		"minos-admin-token":   []byte("admin-token"),
		// Intentionally missing github-app-token.
	}}
	cfg := core.Config{
		ListenAddr:             ":0",
		BearerSecretRef:        "minos-bearer-secret",
		AdminTokenRef:          "minos-admin-token",
		GithubWebhookSecretRef: "github-webhook-secret",
		Admin:                  core.AdminIdentity{Surface: "discord", SurfaceID: "admin-id"},
		Project: core.ProjectConfig{
			ID:                   "p",
			Backend:              "claude-code",
			PluginImage:          "img",
			DefaultWorkspaceSize: envelope.WorkspaceSmall,
			DefaultBaseBranch:    "main",
			Communication:        envelope.Communication{HermesURL: "http://x", ArgusIngestURL: "http://x", AriadneIngestURL: "http://x"},
			Capabilities: core.CapabilitiesDefaults{
				InjectedCredentials: []envelope.InjectedCredential{
					{EnvVar: "GITHUB_TOKEN", CredentialsRef: "github-app-token"},
				},
			},
		},
	}
	_ = kit // silence unused
	store := memstore.New(nil)
	srv, err := core.New(cfg, prov, store, fakedispatch.New(), audit.NewWriterEmitter("t", discardWriter{}))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	req := core.CommissionRequest{
		TaskType:  envelope.TaskTypeCode,
		Brief:     envelope.Brief{Summary: "x"},
		Execution: core.ExecutionRequest{RepoURL: "https://example.com/r", Branch: "f/a"},
	}
	_, err = srv.Commission(context.Background(), req)
	if err == nil {
		t.Fatal("expected credential resolution failure to propagate")
	}
	// Task should still exist but in failed state.
	tasks, _ := store.ListTasks(context.Background(), nil, 0)
	if len(tasks) != 1 || tasks[0].State != storage.StateFailed {
		t.Errorf("expected single failed task, got %+v", tasks)
	}
	var _ dispatch.Dispatcher = fakedispatch.New() // keep dispatch import load-bearing for this file
}
