package pipeline

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// A test that goes without its cache gets an empty folder at /cache, and
// keeps its other volumes.
func TestWithoutCache(t *testing.T) {
	pod := &corev1.Pod{Spec: corev1.PodSpec{Volumes: []corev1.Volume{
		{Name: "src", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: "cache", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "cache-ai-platform"}}},
	}}}
	withoutCache(pod)
	if v := pod.Spec.Volumes[1]; v.PersistentVolumeClaim != nil || v.EmptyDir == nil {
		t.Fatalf("cache volume %+v", v)
	}
	if pod.Spec.Volumes[0].EmptyDir == nil {
		t.Fatal("the other volume changed")
	}
}

// A test waits for another run's test to let the cache go, up to a limit.
func TestWaitCacheFree(t *testing.T) {
	ctx := context.Background()
	step := Step{ID: "platform:test", App: "ai"}
	other := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "otra", Namespace: "b"},
		Spec: corev1.PodSpec{NodeName: "worker1", Volumes: []corev1.Volume{{Name: "cache", VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: cacheName(step)}}}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning}}
	cs := fake.NewSimpleClientset(other)
	k := &KubeExecutor{Client: cs, Namespace: "b"}
	if k.waitCacheFree(ctx, "esta", step, 0) {
		t.Fatal("free while another test has it")
	}
	_ = cs.CoreV1().Pods("b").Delete(ctx, "otra", metav1.DeleteOptions{})
	if !k.waitCacheFree(ctx, "esta", step, 0) {
		t.Fatal("still in use once the other test is gone")
	}
}
