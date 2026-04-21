package k3s_test

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/GoodOlClint/daedalus/minos/dispatch"
	"github.com/GoodOlClint/daedalus/minos/dispatch/k3s"
)

func makeSpec() dispatch.PodSpec {
	return dispatch.PodSpec{
		Name:      "daedalus-abcd-efgh",
		Namespace: "daedalus-test",
		Labels: map[string]string{
			"daedalus.project/pod-class": "daedalus",
		},
		Image:         "ghcr.io/example/plugin:latest",
		Envelope:      []byte(`{"schema_version":"1"}`),
		PlainEnv:      map[string]string{"DAEDALUS_TASK_ID": "t-1"},
		SecretEnv:     map[string]string{"GITHUB_TOKEN": "ghs_fake"},
		CPURequest:    "500m",
		CPULimit:      "2",
		MemoryRequest: "2Gi",
		MemoryLimit:   "4Gi",
		EphemeralDisk: "20Gi",
	}
}

func TestSpawnCreatesSecretAndPod(t *testing.T) {
	cli := fake.NewSimpleClientset()
	d := k3s.NewWithClient(cli)
	ctx := context.Background()

	spec := makeSpec()
	if err := d.SpawnPod(ctx, spec); err != nil {
		t.Fatalf("spawn: %v", err)
	}

	secret, err := cli.CoreV1().Secrets(spec.Namespace).Get(ctx, spec.Name+"-envelope", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if string(secret.Data["envelope.json"]) != string(spec.Envelope) {
		t.Errorf("envelope not written to secret")
	}
	if string(secret.Data["GITHUB_TOKEN"]) != "ghs_fake" {
		t.Errorf("credential not written to secret")
	}

	pod, err := cli.CoreV1().Pods(spec.Namespace).Get(ctx, spec.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get pod: %v", err)
	}
	if pod.Spec.Containers[0].Image != spec.Image {
		t.Errorf("image: %s", pod.Spec.Containers[0].Image)
	}
	// Plain env present.
	found := false
	for _, e := range pod.Spec.Containers[0].Env {
		if e.Name == "DAEDALUS_TASK_ID" && e.Value == "t-1" {
			found = true
		}
	}
	if !found {
		t.Errorf("plain env not injected")
	}
	// Secret env via SecretKeyRef.
	foundSecret := false
	for _, e := range pod.Spec.Containers[0].Env {
		if e.Name == "GITHUB_TOKEN" && e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil {
			foundSecret = true
		}
	}
	if !foundSecret {
		t.Errorf("secret env not wired to SecretKeyRef")
	}
	// Volume mounts present.
	mounts := pod.Spec.Containers[0].VolumeMounts
	if len(mounts) < 3 {
		t.Errorf("expected 3 volume mounts, got %d", len(mounts))
	}
}

func TestSpawnRollsBackSecretOnPodCreateFailure(t *testing.T) {
	cli := fake.NewSimpleClientset()
	// Simulate a pre-existing pod so Create returns AlreadyExists.
	spec := makeSpec()
	_, _ = cli.CoreV1().Pods(spec.Namespace).Create(context.Background(), &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: spec.Name, Namespace: spec.Namespace},
	}, metav1.CreateOptions{})

	d := k3s.NewWithClient(cli)
	if err := d.SpawnPod(context.Background(), spec); err == nil {
		t.Fatal("expected pod-create conflict")
	}
	// Secret should have been cleaned up.
	_, err := cli.CoreV1().Secrets(spec.Namespace).Get(context.Background(), spec.Name+"-envelope", metav1.GetOptions{})
	if err == nil {
		t.Errorf("orphan secret remained after pod create failure")
	}
}

func TestDeletePodRemovesBoth(t *testing.T) {
	cli := fake.NewSimpleClientset()
	d := k3s.NewWithClient(cli)
	ctx := context.Background()
	spec := makeSpec()
	if err := d.SpawnPod(ctx, spec); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if err := d.DeletePod(ctx, spec.Namespace, spec.Name); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := cli.CoreV1().Pods(spec.Namespace).Get(ctx, spec.Name, metav1.GetOptions{}); err == nil {
		t.Errorf("pod still present after delete")
	}
	if _, err := cli.CoreV1().Secrets(spec.Namespace).Get(ctx, spec.Name+"-envelope", metav1.GetOptions{}); err == nil {
		t.Errorf("secret still present after delete")
	}
}

func TestDeletePodMissing(t *testing.T) {
	d := k3s.NewWithClient(fake.NewSimpleClientset())
	err := d.DeletePod(context.Background(), "nowhere", "nobody")
	if !errors.Is(err, dispatch.ErrPodNotFound) {
		t.Errorf("expected ErrPodNotFound, got %v", err)
	}
}

func TestPodPhase(t *testing.T) {
	cli := fake.NewSimpleClientset()
	ctx := context.Background()
	_, _ = cli.CoreV1().Pods("ns").Create(ctx, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}, metav1.CreateOptions{})

	d := k3s.NewWithClient(cli)
	phase, err := d.PodPhase(ctx, "ns", "p")
	if err != nil {
		t.Fatalf("phase: %v", err)
	}
	if phase != dispatch.PhaseRunning {
		t.Errorf("expected Running, got %s", phase)
	}

	_, err = d.PodPhase(ctx, "ns", "missing")
	if !errors.Is(err, dispatch.ErrPodNotFound) {
		t.Errorf("expected ErrPodNotFound for missing pod, got %v", err)
	}
}
