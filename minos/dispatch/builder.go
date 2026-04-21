package dispatch

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/GoodOlClint/daedalus/pkg/envelope"
	"github.com/GoodOlClint/daedalus/pkg/provider"
)

// EnvelopePath is where the dispatcher mounts envelope.json inside the pod.
// The plugin entry script reads DAEDALUS_ENVELOPE to find this path.
const EnvelopePath = "/var/run/daedalus/envelope.json"

// BuilderInput carries everything the spec builder needs beyond the
// envelope itself.
type BuilderInput struct {
	Envelope      *envelope.Envelope
	TaskID        uuid.UUID
	RunID         uuid.UUID
	Namespace     string
	Image         string
	ProjectID     string
	WorkspaceSize envelope.WorkspaceSize

	// MinosURL is the pod-reachable URL of the Minos HTTP API (for the
	// /tasks/{id}/pr callback). Empty is acceptable but disables the
	// pod → Minos PR-report path.
	MinosURL string

	// ArgusSidecarImage, when set, adds the Argus heartbeat sidecar
	// container to the pod. Empty disables the sidecar (Slice A posture).
	ArgusSidecarImage string

	// Resolver fetches credential plaintext for each InjectedCredential in
	// the envelope. Typical implementation is the secret provider.
	Resolver CredentialResolver
}

// CredentialResolver is satisfied by pkg/provider.Provider; scoped to the
// narrow Resolve operation the builder needs so tests can stub trivially.
type CredentialResolver interface {
	Resolve(ctx context.Context, ref string) (*provider.Value, error)
}

// BuildPodSpec composes a PodSpec from a task envelope and project inputs.
// It resolves every InjectedCredential through the provider and embeds the
// envelope JSON so the pod can consume it from the Secret-backed file.
func BuildPodSpec(ctx context.Context, in BuilderInput) (PodSpec, error) {
	if in.Envelope == nil {
		return PodSpec{}, fmt.Errorf("dispatch: envelope required")
	}
	if in.Image == "" {
		return PodSpec{}, fmt.Errorf("dispatch: image required")
	}
	if in.Namespace == "" {
		in.Namespace = "daedalus"
	}

	envJSON, err := json.Marshal(in.Envelope)
	if err != nil {
		return PodSpec{}, fmt.Errorf("dispatch: marshal envelope: %w", err)
	}

	secretEnv := make(map[string]string, len(in.Envelope.Capabilities.InjectedCredentials))
	for _, ic := range in.Envelope.Capabilities.InjectedCredentials {
		val, err := in.Resolver.Resolve(ctx, ic.CredentialsRef)
		if err != nil {
			return PodSpec{}, fmt.Errorf("dispatch: resolve credential %s: %w", ic.CredentialsRef, err)
		}
		secretEnv[ic.EnvVar] = string(val.Data)
	}

	plainEnv := map[string]string{
		"DAEDALUS_ENVELOPE":       EnvelopePath,
		"DAEDALUS_TASK_ID":        in.TaskID.String(),
		"DAEDALUS_RUN_ID":         in.RunID.String(),
		"DAEDALUS_PROJECT_ID":     in.ProjectID,
		"DAEDALUS_THREAD_URL":     in.Envelope.Communication.HermesURL,
		"DAEDALUS_ARGUS_INGEST":   in.Envelope.Communication.ArgusIngestURL,
		"DAEDALUS_ARIADNE_INGEST": in.Envelope.Communication.AriadneIngestURL,
	}
	if in.MinosURL != "" {
		plainEnv["DAEDALUS_MINOS_URL"] = in.MinosURL
	}
	if in.Envelope.Capabilities.McpAuthToken != "" {
		secretEnv["MCP_AUTH_TOKEN"] = in.Envelope.Capabilities.McpAuthToken
	}

	labels := map[string]string{
		"daedalus.project/pod-class":  "daedalus",
		"daedalus.project/project-id": in.ProjectID,
		"daedalus.project/task-id":    in.TaskID.String(),
		"daedalus.project/run-id":     in.RunID.String(),
	}

	size := in.WorkspaceSize
	if size == "" {
		size = in.Envelope.Execution.WorkspaceSize
	}
	disk := ephemeralForSize(size)

	var sidecars []Sidecar
	if in.ArgusSidecarImage != "" {
		sidecars = append(sidecars, Sidecar{
			Name:  "argus-sidecar",
			Image: in.ArgusSidecarImage,
		})
	}

	return PodSpec{
		Name:          "daedalus-" + in.TaskID.String()[:8] + "-" + in.RunID.String()[:8],
		Namespace:     in.Namespace,
		Labels:        labels,
		Image:         in.Image,
		Envelope:      envJSON,
		PlainEnv:      plainEnv,
		SecretEnv:     secretEnv,
		CPURequest:    "500m",
		CPULimit:      "2",
		MemoryRequest: "2Gi",
		MemoryLimit:   "4Gi",
		EphemeralDisk: disk,
		Sidecars:      sidecars,
	}, nil
}

// ephemeralForSize maps the logical workspace size to the ephemeral disk
// allotment per architecture.md §16 Workspace Sizing.
func ephemeralForSize(s envelope.WorkspaceSize) string {
	switch s {
	case envelope.WorkspaceLarge:
		return "100Gi"
	case envelope.WorkspaceMedium:
		return "50Gi"
	default:
		return "20Gi"
	}
}
