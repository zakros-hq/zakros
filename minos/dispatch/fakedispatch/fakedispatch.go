// Package fakedispatch is a fake dispatch.Dispatcher for tests and local
// dev. Pods are tracked in-memory; no Kubernetes API is touched.
package fakedispatch

import (
	"context"
	"fmt"
	"sync"

	"github.com/GoodOlClint/daedalus/minos/dispatch"
)

// Pod captures what the fake dispatcher remembers about a spawned pod.
type Pod struct {
	Spec  dispatch.PodSpec
	Phase dispatch.Phase
}

// Dispatcher is the in-memory implementation.
type Dispatcher struct {
	mu   sync.Mutex
	pods map[string]*Pod

	// SpawnError, when non-nil, is returned from SpawnPod — useful for
	// exercising the failed-dispatch path in tests.
	SpawnError error
}

// New returns a ready Dispatcher.
func New() *Dispatcher {
	return &Dispatcher{pods: make(map[string]*Pod)}
}

// SpawnPod records the pod.
func (d *Dispatcher) SpawnPod(_ context.Context, spec dispatch.PodSpec) error {
	if d.SpawnError != nil {
		return d.SpawnError
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	key := spec.Namespace + "/" + spec.Name
	if _, exists := d.pods[key]; exists {
		return fmt.Errorf("pod %s already exists", key)
	}
	d.pods[key] = &Pod{Spec: spec, Phase: dispatch.PhasePending}
	return nil
}

// DeletePod removes the pod.
func (d *Dispatcher) DeletePod(_ context.Context, namespace, name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	key := namespace + "/" + name
	if _, ok := d.pods[key]; !ok {
		return dispatch.ErrPodNotFound
	}
	delete(d.pods, key)
	return nil
}

// PodPhase returns the current phase.
func (d *Dispatcher) PodPhase(_ context.Context, namespace, name string) (dispatch.Phase, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	p, ok := d.pods[namespace+"/"+name]
	if !ok {
		return dispatch.PhaseUnknown, dispatch.ErrPodNotFound
	}
	return p.Phase, nil
}

// SetPhase is a test helper that mutates a pod's phase in-place (simulates
// k8s-driven state changes).
func (d *Dispatcher) SetPhase(namespace, name string, phase dispatch.Phase) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if p, ok := d.pods[namespace+"/"+name]; ok {
		p.Phase = phase
	}
}

// Pods returns a snapshot of all pods currently tracked.
func (d *Dispatcher) Pods() []Pod {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Pod, 0, len(d.pods))
	for _, p := range d.pods {
		out = append(out, *p)
	}
	return out
}
