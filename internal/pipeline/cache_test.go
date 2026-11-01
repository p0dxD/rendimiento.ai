package pipeline

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
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
