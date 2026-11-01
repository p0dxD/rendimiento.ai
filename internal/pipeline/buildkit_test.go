package pipeline

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// An image whose favourite daemon is on a node that takes no runs builds on
// another one; with every daemon's node off, the pool is used as it is.
func TestBuildkitSkipsNodesOff(t *testing.T) {
	daemon := func(name, node string) *corev1.Pod {
		return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "devops-tools"}, Spec: corev1.PodSpec{NodeName: node}}
	}
	cs := fake.NewSimpleClientset(daemon("buildkitd-0", "colorpi"), daemon("buildkitd-1", "worker1"), daemon("buildkitd-2", "worker2"))
	k := &KubeExecutor{Client: cs, BuildkitPool: "buildkitd-pool.devops-tools.svc.cluster.local",
		Resolver: fakeSRV{targets: []string{"buildkitd-0", "buildkitd-1", "buildkitd-2"}}}
	ctx := context.Background()
	node := map[string]string{"buildkitd-0": "colorpi", "buildkitd-1": "worker1", "buildkitd-2": "worker2"}
	key := "reg/rendimiento-ai-platform"
	_, fav := k.buildkitFor(ctx, key)
	off := node[fav]
	k.Limits = func() ([]string, int64) { return []string{off}, 0 }
	if _, d := k.buildkitFor(ctx, key); d == fav || d == "" {
		t.Fatalf("with %s off it still builds on %q", off, d)
	}
	k.Limits = func() ([]string, int64) { return []string{"colorpi", "worker1", "worker2"}, 0 }
	if _, d := k.buildkitFor(ctx, key); d != fav {
		t.Fatalf("with every node off it should keep the pool and %s, got %q", fav, d)
	}
}
