package pipeline

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// A container is done when its status says it terminated, or its pod is
// gone; a log stream left open after that is closed.
func TestContainerEnded(t *testing.T) {
	ctx := context.Background()
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "b"}, Status: corev1.PodStatus{
		InitContainerStatuses: []corev1.ContainerStatus{{Name: "clone", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{}}}},
		ContainerStatuses:     []corev1.ContainerStatus{{Name: "step", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}},
	}}
	k := &KubeExecutor{Client: fake.NewSimpleClientset(pod), Namespace: "b"}
	if !k.containerEnded(ctx, "p", "clone") || k.containerEnded(ctx, "p", "step") || k.containerEnded(ctx, "p", "other") {
		t.Fatal("clone ended, step runs, other is unknown")
	}
	if !k.containerEnded(ctx, "gone", "step") {
		t.Fatal("a pod that is gone has ended")
	}
}
