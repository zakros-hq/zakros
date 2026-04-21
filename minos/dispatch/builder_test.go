package dispatch_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/GoodOlClint/daedalus/minos/dispatch"
	"github.com/GoodOlClint/daedalus/pkg/envelope"
	"github.com/GoodOlClint/daedalus/pkg/provider"
)

type stubResolver map[string][]byte

func (s stubResolver) Resolve(_ context.Context, ref string) (*provider.Value, error) {
	v, ok := s[ref]
	if !ok {
		return nil, provider.ErrNotFound
	}
	return &provider.Value{Ref: ref, Data: v}, nil
}

func sampleEnvelope(t *testing.T) *envelope.Envelope {
	t.Helper()
	return &envelope.Envelope{
		SchemaVersion: envelope.SchemaVersion,
		ID:            uuid.NewString(),
		ProjectID:     "test-project",
		TaskType:      envelope.TaskTypeCode,
		Backend:       "claude-code",
		Execution: envelope.Execution{
			RepoURL:       "https://github.com/example/widget",
			Branch:        "fix/x",
			BaseBranch:    "main",
			WorkspaceSize: envelope.WorkspaceMedium,
		},
		Communication: envelope.Communication{
			HermesURL:        "http://minos/hermes",
			ArgusIngestURL:   "http://minos/argus",
			AriadneIngestURL: "http://ariadne/ingest",
		},
		Capabilities: envelope.Capabilities{
			InjectedCredentials: []envelope.InjectedCredential{
				{EnvVar: "GITHUB_TOKEN", CredentialsRef: "github-token"},
			},
			McpAuthToken: "placeholder",
		},
		Inputs:     json.RawMessage(`{}`),
		Acceptance: json.RawMessage(`{}`),
	}
}

func TestBuildPodSpec(t *testing.T) {
	taskID := uuid.New()
	runID := uuid.New()
	in := dispatch.BuilderInput{
		Envelope:      sampleEnvelope(t),
		TaskID:        taskID,
		RunID:         runID,
		Namespace:     "daedalus-test",
		Image:         "ghcr.io/example/plugin:latest",
		ProjectID:     "test-project",
		WorkspaceSize: envelope.WorkspaceMedium,
		Resolver:      stubResolver{"github-token": []byte("ghs_fake")},
	}
	spec, err := dispatch.BuildPodSpec(context.Background(), in)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if spec.Namespace != "daedalus-test" {
		t.Errorf("namespace: %s", spec.Namespace)
	}
	if spec.Image != in.Image {
		t.Errorf("image: %s", spec.Image)
	}
	if got := spec.SecretEnv["GITHUB_TOKEN"]; got != "ghs_fake" {
		t.Errorf("credential not resolved: %q", got)
	}
	if spec.PlainEnv["DAEDALUS_ENVELOPE"] != dispatch.EnvelopePath {
		t.Errorf("envelope path not injected: %s", spec.PlainEnv["DAEDALUS_ENVELOPE"])
	}
	if spec.PlainEnv["DAEDALUS_TASK_ID"] != taskID.String() {
		t.Errorf("task id not injected")
	}
	if spec.EphemeralDisk != "50Gi" {
		t.Errorf("medium workspace should be 50Gi, got %s", spec.EphemeralDisk)
	}
	if spec.Labels["daedalus.project/task-id"] != taskID.String() {
		t.Errorf("task-id label missing")
	}
	// Envelope JSON round-trips.
	var env envelope.Envelope
	if err := json.Unmarshal(spec.Envelope, &env); err != nil {
		t.Fatalf("envelope JSON: %v", err)
	}
	if env.ProjectID != "test-project" {
		t.Errorf("envelope round-trip lost data")
	}
}

func TestBuildPodSpecResolverError(t *testing.T) {
	in := dispatch.BuilderInput{
		Envelope:  sampleEnvelope(t),
		TaskID:    uuid.New(),
		RunID:     uuid.New(),
		Namespace: "daedalus-test",
		Image:     "img",
		ProjectID: "p",
		Resolver:  stubResolver{}, // no credential registered — should error
	}
	if _, err := dispatch.BuildPodSpec(context.Background(), in); err == nil {
		t.Fatal("expected resolver failure")
	}
}
