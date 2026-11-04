package destino

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/p0dxD/rendimiento.ai/api/v1alpha1"
	"github.com/p0dxD/rendimiento.ai/internal/spec"
	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// The cluster as a destination: an app is an App object, a release is
// written into it, and its rollout is read from what the controller reports.
func TestKubernetes(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	kube := fake.NewClientBuilder().WithScheme(scheme).Build()
	var d Destino = Kubernetes{Kube: kube}

	sp := spec.Spec{Services: []spec.Service{{Name: "web"}}}
	sp.Default()
	if err := d.Crear(ctx, &store.App{ID: 7, Name: "tienda", Repo: "p0dxD/tienda", Spec: sp}, false); err != nil {
		t.Fatal(err)
	}
	var cr v1alpha1.App
	if err := kube.Get(ctx, client.ObjectKey{Name: "tienda"}, &cr); err != nil || cr.Labels["rendimiento.ai/app-id"] != "7" {
		t.Fatalf("App object: %v %v", err, cr.Labels)
	}

	rel := &store.Release{Number: 3, Spec: sp, Images: map[string]string{"web": "registry.example.lan/tienda@sha256:abc"}}
	if err := d.Aplicar(ctx, "tienda", rel); err != nil {
		t.Fatal(err)
	}
	if st, msg, err := d.Estado(ctx, "tienda", 3); err != nil || st != Pendiente || msg == "" {
		t.Fatalf("before the controller: %v %q %v", st, msg, err)
	}

	report := func(phase v1alpha1.Phase, msg string) {
		t.Helper()
		if err := kube.Get(ctx, client.ObjectKey{Name: "tienda"}, &cr); err != nil {
			t.Fatal(err)
		}
		cr.Status.Release, cr.Status.ObservedGeneration, cr.Status.Phase = 3, cr.Generation, phase
		cr.Status.Services = []v1alpha1.ServiceStatus{{Name: "web", Message: msg}}
		if err := kube.Update(ctx, &cr); err != nil {
			t.Fatal(err)
		}
	}
	report(v1alpha1.PhaseHealthy, "")
	if st, _, _ := d.Estado(ctx, "tienda", 3); st != Sana {
		t.Fatalf("healthy: %v", st)
	}
	report(v1alpha1.PhaseDegraded, "crash loop")
	if st, msg, _ := d.Estado(ctx, "tienda", 3); st != Fallida || msg != "web: crash loop" {
		t.Fatalf("degraded: %v %q", st, msg)
	}
	if st, _, _ := d.Estado(ctx, "tienda", 2); st != Reemplazada {
		t.Fatalf("an older release: %v", st)
	}

	if err := d.Quitar(ctx, "tienda"); err != nil {
		t.Fatal(err)
	}
	if err := d.Quitar(ctx, "tienda"); err != nil {
		t.Fatalf("removing it again: %v", err)
	}
}
