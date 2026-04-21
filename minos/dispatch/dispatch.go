// Package dispatch turns a committed task envelope into a running Daedalus
// pod. The Dispatcher interface decouples Minos from the concrete Kubernetes
// client, so tests run against fakedispatch and production uses the client-go
// implementation in minos/dispatch/k3s.
package dispatch

import (
	"context"
	"errors"
)

// ErrPodNotFound is returned by Dispatcher operations when a named pod
// does not exist (caller should treat as "already terminated").
var ErrPodNotFound = errors.New("pod not found")

// Phase mirrors a subset of Kubernetes PodPhase — the values Minos cares
// about. Phase.Terminal reports whether no further transitions are expected.
type Phase string

const (
	PhaseUnknown   Phase = ""
	PhasePending   Phase = "Pending"
	PhaseRunning   Phase = "Running"
	PhaseSucceeded Phase = "Succeeded"
	PhaseFailed    Phase = "Failed"
)

// Terminal reports whether the phase is one the pod will not leave.
func (p Phase) Terminal() bool {
	return p == PhaseSucceeded || p == PhaseFailed
}

// PodSpec is the minimum information needed to spawn a Daedalus pod.
// Kubernetes-specific details (affinity, tolerations, service accounts,
// sidecars) live in the concrete Dispatcher implementation — this type
// is deliberately kept narrow so the envelope composer has no k8s deps.
type PodSpec struct {
	Name      string
	Namespace string
	Labels    map[string]string

	// Image is the plugin image (architecture.md §8 Daedalus Agents);
	// Phase 1 is the Claude Code plugin image declared in ProjectConfig.
	Image string

	// Envelope is the full task-envelope JSON. The dispatcher delivers
	// it to the pod as a Secret-backed file at /var/run/daedalus/envelope.json
	// (path defined by EnvelopePath on the pod side).
	Envelope []byte

	// PlainEnv is non-sensitive environment (workspace, thread URL, etc.).
	PlainEnv map[string]string

	// SecretEnv is sensitive environment (GITHUB_TOKEN, etc.) — stored in
	// the same Secret as Envelope; entries land as-is in the pod's env.
	SecretEnv map[string]string

	// Resource knobs per architecture.md §16 Pod Resource Limits.
	CPURequest    string
	CPULimit      string
	MemoryRequest string
	MemoryLimit   string
	EphemeralDisk string

	// Sidecars are additional containers co-scheduled alongside the
	// worker. Phase 1 uses this for the Argus heartbeat sidecar; Slice B
	// adds the thread sidecar.
	Sidecars []Sidecar
}

// Sidecar describes a companion container for a pod. Sidecars share the
// pod network namespace and the envelope/secret volumes.
type Sidecar struct {
	Name string
	Image string
	// Env: additional env vars beyond the base PlainEnv/SecretEnv the
	// worker gets; sidecars inherit the same injected secrets via
	// SecretKeyRef bindings built by the dispatcher.
	Env map[string]string
}

// Dispatcher spawns, observes, and tears down Daedalus pods.
type Dispatcher interface {
	// SpawnPod creates the pod (and its Secret) and returns once the pod
	// object exists in Kubernetes. It does not block on Running — Argus
	// observes the phase separately.
	SpawnPod(ctx context.Context, spec PodSpec) error

	// DeletePod tears down the pod and its Secret. Returns ErrPodNotFound
	// if the pod has already been removed.
	DeletePod(ctx context.Context, namespace, name string) error

	// PodPhase returns the pod's current phase, or ErrPodNotFound.
	PodPhase(ctx context.Context, namespace, name string) (Phase, error)
}
