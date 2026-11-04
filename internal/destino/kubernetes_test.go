package destino

import (
	"context"
	"errors"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/p0dxD/rendimiento.ai/api/v1alpha1"
	"github.com/p0dxD/rendimiento.ai/internal/render"
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

// The app's page from the cluster: its status, the resource tree with each
// part's health, a restart, and what deleting it would remove.
func TestKubernetesPaginas(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	_ = clientgoscheme.AddToScheme(scheme)
	one := int32(1)
	labels := map[string]string{render.LabelApp: "tienda", render.LabelService: "web"}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&v1alpha1.App{ObjectMeta: metav1.ObjectMeta{Name: "tienda"}, Spec: v1alpha1.AppSpec{Suspend: true}, Status: v1alpha1.AppStatus{Release: 4}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "tienda", Labels: labels},
			Spec:   appsv1.DeploymentSpec{Replicas: &one},
			Status: appsv1.DeploymentStatus{ReadyReplicas: 1, UpdatedReplicas: 1}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "web-1", Namespace: "tienda", Labels: labels},
			Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{RestartCount: 2, State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}}}}},
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "web-data", Namespace: "tienda", Labels: labels},
			Spec: corev1.PersistentVolumeClaimSpec{Resources: corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")}}}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "llaves", Namespace: "tienda"}, Type: corev1.SecretTypeOpaque},
	).Build()
	d := Kubernetes{Kube: kube}

	if st, err := d.Situacion(ctx, "tienda"); err != nil || !st.Suspendida || st.Status.Release != 4 {
		t.Fatalf("situacion: %+v %v", st, err)
	}
	sp := spec.Spec{Services: []spec.Service{{Name: "web"}}}
	root, err := d.Recursos(ctx, &store.App{Name: "tienda", Spec: sp})
	if err != nil || len(root.Children) != 1 {
		t.Fatalf("recursos: %+v %v", root, err)
	}
	web := root.Children[0]
	if web.Health != "Healthy" || len(web.Children) != 2 || web.Children[0].Children[0].Health != "Degraded" || web.Children[1].Kind != "Volume" {
		t.Fatalf("web: %+v", web)
	}

	if err := d.Reiniciar(ctx, "tienda"); err != nil {
		t.Fatal(err)
	}
	var dep appsv1.Deployment
	if err := kube.Get(ctx, client.ObjectKey{Namespace: "tienda", Name: "web"}, &dep); err != nil || dep.Spec.Template.Annotations["rendimiento.ai/restartedAt"] == "" {
		t.Fatalf("restart: %v %v", err, dep.Spec.Template.Annotations)
	}

	im, err := d.Impacto(ctx, "tienda")
	if err != nil || len(im.Volumes) != 1 || im.Volumes[0] != "web-data (1Gi)" || len(im.Secrets) != 1 {
		t.Fatalf("impacto: %+v %v", im, err)
	}
	if err := d.Desconectar(ctx, "nadie"); err != nil {
		t.Fatalf("disconnecting an app that is not there: %v", err)
	}
}

// An app's secrets live in its namespace: none before it exists, keys
// listed without values, and a nil value removes its key.
func TestKubernetesSecretos(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	kube := fake.NewClientBuilder().WithScheme(scheme).Build()
	d := Kubernetes{Kube: kube}
	v := func(s string) *string { return &s }

	if _, err := d.LlavesSecreto(ctx, "tienda", "llaves"); !errors.Is(err, ErrSinLugar) {
		t.Fatalf("before the namespace: %v", err)
	}
	if err := kube.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "tienda", Labels: map[string]string{render.LabelApp: "tienda"}}}); err != nil {
		t.Fatal(err)
	}
	if keys, err := d.LlavesSecreto(ctx, "tienda", "llaves"); err != nil || len(keys) != 0 {
		t.Fatalf("not set yet: %v %v", keys, err)
	}
	if err := d.GuardarSecreto(ctx, "tienda", "llaves", map[string]*string{"B": v("2"), "A": v("1")}); err != nil {
		t.Fatal(err)
	}
	if err := d.GuardarSecreto(ctx, "tienda", "llaves", map[string]*string{"A": nil, "C": v("3")}); err != nil {
		t.Fatal(err)
	}
	if keys, err := d.LlavesSecreto(ctx, "tienda", "llaves"); err != nil || strings.Join(keys, ",") != "B,C" {
		t.Fatalf("keys: %v %v", keys, err)
	}
}
