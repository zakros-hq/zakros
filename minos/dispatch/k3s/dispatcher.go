// Package k3s is the production dispatch.Dispatcher backed by client-go.
// It targets the single-node Labyrinth k3s cluster per architecture.md §16.
package k3s

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/GoodOlClint/daedalus/minos/dispatch"
)

// Dispatcher implements dispatch.Dispatcher against a Kubernetes API.
type Dispatcher struct {
	client kubernetes.Interface
}

// NewFromKubeconfig builds a Dispatcher from a kubeconfig path. Empty path
// falls back to in-cluster config for pods (Minos itself in Phase 2+).
func NewFromKubeconfig(path string) (*Dispatcher, error) {
	var (
		cfg *rest.Config
		err error
	)
	if path == "" {
		cfg, err = rest.InClusterConfig()
	} else {
		cfg, err = clientcmd.BuildConfigFromFlags("", path)
	}
	if err != nil {
		return nil, fmt.Errorf("k3s: build rest config: %w", err)
	}
	cli, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("k3s: build client: %w", err)
	}
	return &Dispatcher{client: cli}, nil
}

// NewWithClient wraps an existing client — used by tests with fake clients.
func NewWithClient(cli kubernetes.Interface) *Dispatcher {
	return &Dispatcher{client: cli}
}

// SpawnPod creates the Secret holding the envelope + resolved credentials,
// then creates the Pod that consumes them. Both objects live in the task
// namespace and are labeled so DeletePod can find them.
func (d *Dispatcher) SpawnPod(ctx context.Context, spec dispatch.PodSpec) error {
	if spec.Name == "" || spec.Namespace == "" {
		return fmt.Errorf("k3s: spec missing name/namespace")
	}
	secret := buildSecret(spec)
	if _, err := d.client.CoreV1().Secrets(spec.Namespace).Create(ctx, secret, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("k3s: create secret: %w", err)
	}
	pod := buildPod(spec)
	if _, err := d.client.CoreV1().Pods(spec.Namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		// Best effort: clean up the Secret we just wrote.
		_ = d.client.CoreV1().Secrets(spec.Namespace).Delete(ctx, secret.Name, metav1.DeleteOptions{})
		return fmt.Errorf("k3s: create pod: %w", err)
	}
	return nil
}

// DeletePod removes both the pod and its associated Secret. Returns
// dispatch.ErrPodNotFound when neither exists.
func (d *Dispatcher) DeletePod(ctx context.Context, namespace, name string) error {
	podErr := d.client.CoreV1().Pods(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	secretErr := d.client.CoreV1().Secrets(namespace).Delete(ctx, secretName(name), metav1.DeleteOptions{})
	switch {
	case podErr == nil:
		// Pod existed; ignore secret 404.
		return nil
	case apierrors.IsNotFound(podErr) && apierrors.IsNotFound(secretErr):
		return dispatch.ErrPodNotFound
	case apierrors.IsNotFound(podErr):
		// Orphan Secret was cleaned up; no pod ever existed.
		return dispatch.ErrPodNotFound
	default:
		return fmt.Errorf("k3s: delete pod: %w", podErr)
	}
}

// PodPhase returns the current phase reported by the API server.
func (d *Dispatcher) PodPhase(ctx context.Context, namespace, name string) (dispatch.Phase, error) {
	pod, err := d.client.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return dispatch.PhaseUnknown, dispatch.ErrPodNotFound
		}
		return dispatch.PhaseUnknown, fmt.Errorf("k3s: get pod: %w", err)
	}
	return dispatch.Phase(pod.Status.Phase), nil
}

// secretName is the deterministic Secret name for a given pod.
func secretName(podName string) string {
	return podName + "-envelope"
}

func buildSecret(spec dispatch.PodSpec) *corev1.Secret {
	data := map[string][]byte{"envelope.json": spec.Envelope}
	for k, v := range spec.SecretEnv {
		data[k] = []byte(v)
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName(spec.Name),
			Namespace: spec.Namespace,
			Labels:    spec.Labels,
		},
		Type: corev1.SecretTypeOpaque,
		Data: data,
	}
}

func buildPod(spec dispatch.PodSpec) *corev1.Pod {
	envVars := make([]corev1.EnvVar, 0, len(spec.PlainEnv)+len(spec.SecretEnv))
	for k, v := range spec.PlainEnv {
		envVars = append(envVars, corev1.EnvVar{Name: k, Value: v})
	}
	for k := range spec.SecretEnv {
		envVars = append(envVars, corev1.EnvVar{
			Name: k,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: secretName(spec.Name)},
					Key:                  k,
				},
			},
		})
	}

	resources := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{},
		Limits:   corev1.ResourceList{},
	}
	if q, err := resource.ParseQuantity(spec.CPURequest); err == nil {
		resources.Requests[corev1.ResourceCPU] = q
	}
	if q, err := resource.ParseQuantity(spec.MemoryRequest); err == nil {
		resources.Requests[corev1.ResourceMemory] = q
	}
	if q, err := resource.ParseQuantity(spec.CPULimit); err == nil {
		resources.Limits[corev1.ResourceCPU] = q
	}
	if q, err := resource.ParseQuantity(spec.MemoryLimit); err == nil {
		resources.Limits[corev1.ResourceMemory] = q
	}
	if q, err := resource.ParseQuantity(spec.EphemeralDisk); err == nil {
		resources.Limits[corev1.ResourceEphemeralStorage] = q
	}

	containers := []corev1.Container{
		{
			Name:      "worker",
			Image:     spec.Image,
			Env:       envVars,
			Resources: resources,
			VolumeMounts: []corev1.VolumeMount{
				{Name: "envelope", MountPath: "/var/run/daedalus", ReadOnly: true},
				{Name: "workspace", MountPath: "/workspace"},
				{Name: "memory", MountPath: "/var/run/daedalus/memory"},
			},
		},
	}
	for _, sc := range spec.Sidecars {
		scEnv := append([]corev1.EnvVar(nil), envVars...)
		for k, v := range sc.Env {
			scEnv = append(scEnv, corev1.EnvVar{Name: k, Value: v})
		}
		containers = append(containers, corev1.Container{
			Name:  sc.Name,
			Image: sc.Image,
			Env:   scEnv,
		})
	}

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      spec.Name,
			Namespace: spec.Namespace,
			Labels:    spec.Labels,
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Volumes: []corev1.Volume{
				{
					Name: "envelope",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: secretName(spec.Name),
							Items:      []corev1.KeyToPath{{Key: "envelope.json", Path: "envelope.json"}},
						},
					},
				},
				{
					Name:         "workspace",
					VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
				},
				{
					Name:         "memory",
					VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
				},
			},
			Containers: containers,
		},
	}
}

// Compile-time interface check.
var _ dispatch.Dispatcher = (*Dispatcher)(nil)
