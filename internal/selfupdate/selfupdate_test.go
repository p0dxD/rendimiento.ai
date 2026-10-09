package selfupdate

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/p0dxD/rendimiento.ai/internal/store"
)

const (
	old = "registry.example.lan:5000/rendimiento-platform@sha256:aaaa"
	neu = "registry.example.lan:5000/rendimiento-platform@sha256:bbbb"
)

type releases []*store.Release

func (r releases) GetApp(_ context.Context, name string) (*store.App, error) {
	if name != "rendimiento" {
		return nil, store.ErrNotFound
	}
	return &store.App{ID: 1, Name: name}, nil
}

func (r releases) ListReleases(context.Context, int64, int) ([]*store.Release, error) { return r, nil }

func updater(t *testing.T, running string) *Updater {
	t.Helper()
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "rendimiento-system", Name: "rendimiento"},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: "rendimiento", Image: "registry.example.lan:5000/rendimiento:latest"},
		}}}},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "rendimiento-system", Name: "rendimiento-abc"},
		Status:     corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: "rendimiento", ImageID: running}}},
	}
	now := time.Now()
	return &Updater{
		Kube: fake.NewClientBuilder().WithScheme(scheme).WithObjects(dep, pod).Build(),
		Releases: releases{
			{Number: 3, SHA: "c3", Images: map[string]string{"docs": "x@sha256:dddd"}, CreatedAt: now}, // no platform image
			{Number: 2, SHA: "c2", Images: map[string]string{"build:platform": neu}, CreatedAt: now},
			{Number: 1, SHA: "c1", Images: map[string]string{"build:platform": old}, CreatedAt: now},
		},
		App: "rendimiento", Build: "platform",
		Namespace: "rendimiento-system", Deployment: "rendimiento", Container: "rendimiento", Pod: "rendimiento-abc",
	}
}

func TestStatus(t *testing.T) {
	ctx := context.Background()
	st, err := updater(t, "registry.example.lan:5000/rendimiento-platform@sha256:aaaa").Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Running != "sha256:aaaa" || st.Current == nil || st.Current.Release != 1 {
		t.Errorf("running %q from %+v, want sha256:aaaa from release 1", st.Running, st.Current)
	}
	if !st.Available || st.Latest == nil || st.Latest.Release != 2 {
		t.Errorf("latest %+v available %v, want release 2 available", st.Latest, st.Available)
	}

	st, _ = updater(t, "registry.example.lan:5000/rendimiento-platform@sha256:bbbb").Status(ctx)
	if st.Available || st.Current == nil || st.Current.Release != 2 {
		t.Errorf("on the latest: available %v current %+v", st.Available, st.Current)
	}

	// Built by hand (make image): no release, so the latest is new.
	st, _ = updater(t, "registry.example.lan:5000/rendimiento@sha256:ffff").Status(ctx)
	if !st.Available || st.Current != nil {
		t.Errorf("hand-built: available %v current %+v", st.Available, st.Current)
	}

	u := updater(t, "")
	u.App = "other"
	if _, err := u.Status(ctx); err == nil {
		t.Error("an app that is not here: want an error")
	}
}

func TestUpdate(t *testing.T) {
	ctx := context.Background()
	u := updater(t, "registry.example.lan:5000/rendimiento-platform@sha256:aaaa")
	if _, err := u.Update(ctx, 3, "ana"); err == nil {
		t.Error("release 3 has no platform image: want an error")
	}
	v, err := u.Update(ctx, 2, "ana")
	if err != nil {
		t.Fatal(err)
	}
	if v.Image != neu {
		t.Errorf("updated to %s, want %s", v.Image, neu)
	}
	var d appsv1.Deployment
	if err := u.Kube.Get(ctx, client.ObjectKey{Namespace: "rendimiento-system", Name: "rendimiento"}, &d); err != nil {
		t.Fatal(err)
	}
	if got := d.Spec.Template.Spec.Containers[0].Image; got != neu {
		t.Errorf("deployment image %s, want %s", got, neu)
	}
	if a := d.Spec.Template.Annotations; a["rendimiento.ai/release"] != "2" || a["rendimiento.ai/updated-by"] != "ana" {
		t.Errorf("annotations %v", a)
	}
}
