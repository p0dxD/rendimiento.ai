package controller

import (
	"context"
	"os"

	"github.com/go-logr/logr"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/p0dxD/rendimiento.ai/api/v1alpha1"
	"github.com/p0dxD/rendimiento.ai/internal/dns"
	"github.com/p0dxD/rendimiento.ai/internal/render"
	"github.com/p0dxD/rendimiento.ai/internal/spec"
)

type fakeDNS struct {
	mu      sync.Mutex
	records map[string]string
}

func (f *fakeDNS) Ensure(_ context.Context, host, app string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records[host] = app
	return nil
}
func (f *fakeDNS) Remove(_ context.Context, host, app string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.records[host] == app {
		delete(f.records, host)
	}
	return nil
}
func (f *fakeDNS) Zones(context.Context) ([]string, error)  { return nil, nil }
func (f *fakeDNS) Describe(context.Context) dns.Description { return dns.Description{ID: "fake"} }
func (f *fakeDNS) has(host string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.records[host]
	return ok
}

var skip = true

func setup(t *testing.T) (client.Client, *fakeDNS) {
	t.Helper()
	ctrl.SetLogger(logr.Discard())
	env := &envtest.Environment{CRDDirectoryPaths: []string{filepath.Join("..", "..", "deploy", "crds"), filepath.Join("testdata", "crds")}, ErrorIfCRDPathMissing: true}
	cfg, err := env.Start()
	if err != nil {
		if os.Getenv("KUBEBUILDER_ASSETS") != "" {
			// Configured but failed to start (e.g. an overloaded machine):
			// fail, so a test gate never passes with these tests skipped.
			t.Fatalf("envtest failed to start: %v", err)
		}
		t.Skipf("envtest unavailable (run setup-envtest and set KUBEBUILDER_ASSETS): %v", err)
	}
	t.Cleanup(func() { _ = env.Stop() })

	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{Scheme: scheme, Metrics: metricsserver.Options{BindAddress: "0"}, Controller: config.Controller{SkipNameValidation: &skip}})
	if err != nil {
		t.Fatal(err)
	}
	dns := &fakeDNS{records: map[string]string{}}
	if err := (&AppReconciler{Client: mgr.GetClient(), Scheme: scheme, Render: render.DefaultOptions(), DNS: dns}).SetupWithManager(mgr); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = mgr.Start(ctx) }()
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	return c, dns
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestAppLifecycle(t *testing.T) {
	c, dns := setup(t)
	ctx := context.Background()
	app := &v1alpha1.App{
		ObjectMeta: metav1.ObjectMeta{Name: "hello"},
		Spec: v1alpha1.AppSpec{
			Repo: "p0dxD/hello",
			Services: []spec.Service{
				{Name: "web", Path: ".", Port: 8080, Size: spec.SizeSmall, Replicas: 2, Domain: "hello.joserod.space", Build: spec.Build{Dockerfile: "Dockerfile"}},
				{Name: "worker", Path: "worker", Port: 8080, Size: spec.SizeSmall, Replicas: 1, Build: spec.Build{Dockerfile: "Dockerfile"}},
			},
		},
	}
	if err := c.Create(ctx, app); err != nil {
		t.Fatal(err)
	}
	get := func() *v1alpha1.App {
		var a v1alpha1.App
		_ = c.Get(ctx, client.ObjectKey{Name: "hello"}, &a)
		return &a
	}
	eventually(t, "WaitingForBuild", func() bool { return get().Status.Phase == v1alpha1.PhaseWaiting })

	// First release arrives from the pipeline.
	updateApp(t, c, "hello", func(a *v1alpha1.App) {
		a.Spec.Images = map[string]string{"web": "registry.local/hello-web@sha256:1", "worker": "registry.local/hello-worker@sha256:1"}
		a.Spec.Release = 1
	})
	var dep appsv1.Deployment
	webKey := client.ObjectKey{Namespace: "hello", Name: "web"}
	eventually(t, "web deployment", func() bool { return c.Get(ctx, webKey, &dep) == nil })
	var ing networkingv1.Ingress
	eventually(t, "web ingress", func() bool { return c.Get(ctx, webKey, &ing) == nil })
	if ing.Annotations["cert-manager.io/cluster-issuer"] != "letsencrypt-prod" || len(ing.OwnerReferences) != 1 {
		t.Fatalf("ingress not rendered as expected: %+v", ing.ObjectMeta)
	}
	eventually(t, "DNS record", func() bool { return dns.has("hello.joserod.space") })
	eventually(t, "Progressing with release 1", func() bool {
		s := get().Status
		return s.Phase == v1alpha1.PhaseProgressing && s.Release == 1 && len(s.Services) == 2
	})

	// Drift: someone scales by hand; the controller puts it back.
	two := int32(2)
	dep.Spec.Replicas = new(int32)
	if err := c.Update(ctx, &dep); err != nil {
		t.Fatal(err)
	}
	eventually(t, "self-heal replicas", func() bool {
		_ = c.Get(ctx, webKey, &dep)
		return *dep.Spec.Replicas == two
	})

	// Rollback / new release: repoint the image.
	updateApp(t, c, "hello", func(a *v1alpha1.App) { a.Spec.Images["web"] = "registry.local/hello-web@sha256:0" })
	eventually(t, "image repointed", func() bool {
		_ = c.Get(ctx, webKey, &dep)
		return dep.Spec.Template.Spec.Containers[0].Image == "registry.local/hello-web@sha256:0"
	})

	// Removing a service prunes its objects.
	updateApp(t, c, "hello", func(a *v1alpha1.App) { a.Spec.Services = a.Spec.Services[:1] })
	eventually(t, "worker pruned", func() bool {
		var d appsv1.Deployment
		var s corev1.Service
		return apierrors.IsNotFound(c.Get(ctx, client.ObjectKey{Namespace: "hello", Name: "worker"}, &d)) &&
			apierrors.IsNotFound(c.Get(ctx, client.ObjectKey{Namespace: "hello", Name: "worker"}, &s))
	})

	// Deleting the app removes its DNS record and the finalizer.
	if err := c.Delete(ctx, get()); err != nil {
		t.Fatal(err)
	}
	eventually(t, "app deleted", func() bool {
		return apierrors.IsNotFound(c.Get(ctx, client.ObjectKey{Name: "hello"}, &v1alpha1.App{}))
	})
	if dns.has("hello.joserod.space") {
		t.Fatal("DNS record not removed")
	}
}

func TestRefusesForeignNamespaceAndHost(t *testing.T) {
	c, _ := setup(t)
	ctx := context.Background()
	// An app still deployed the old way (Jenkins + ArgoCD).
	if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "secplus"}}); err != nil {
		t.Fatal(err)
	}
	pt := networkingv1.PathTypePrefix
	if err := c.Create(ctx, &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "secplus", Namespace: "secplus"},
		Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{Host: "secplus.joserod.space",
			IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{Paths: []networkingv1.HTTPIngressPath{{
				Path: "/", PathType: &pt, Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: "x", Port: networkingv1.ServiceBackendPort{Number: 80}}}}}}}}}},
	}); err != nil {
		t.Fatal(err)
	}
	svc := spec.Service{Name: "web", Path: ".", Port: 3000, Size: spec.SizeSmall, Replicas: 1, Domain: "secplus.joserod.space", Build: spec.Build{Dockerfile: "Dockerfile"}}
	images := map[string]string{"web": "registry.local/x@sha256:1"}
	for name, want := range map[string]string{"secplus": "namespace \"secplus\" already exists", "secplus-v2": "already served by ingress secplus/secplus"} {
		if err := c.Create(ctx, &v1alpha1.App{ObjectMeta: metav1.ObjectMeta{Name: name}, Spec: v1alpha1.AppSpec{Services: []spec.Service{svc}, Images: images}}); err != nil {
			t.Fatal(err)
		}
		eventually(t, name+" blocked", func() bool {
			var a v1alpha1.App
			_ = c.Get(ctx, client.ObjectKey{Name: name}, &a)
			return a.Status.Phase == v1alpha1.PhaseError && strings.Contains(a.Status.Message, want)
		})
	}
	var d appsv1.Deployment
	if !apierrors.IsNotFound(c.Get(ctx, client.ObjectKey{Namespace: "secplus", Name: "web"}, &d)) {
		t.Fatal("must not deploy into a foreign namespace")
	}
}

// Migration of an app deployed another way (ArgoCD): the namespace and its
// secret are kept, new pods start next to the old ones, and traffic moves
// only once they are ready.
func TestAdoptExistingDeployment(t *testing.T) {
	c, _ := setup(t)
	ctx := context.Background()
	one := int32(1)
	pt := networkingv1.PathTypePrefix
	lbl := map[string]string{"app": "legacy"}
	for _, o := range []client.Object{
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "legacy"}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "legacy", Name: "legacy-practice"}, StringData: map[string]string{"token": "x"}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "legacy", Name: "legacy"}, Spec: appsv1.DeploymentSpec{Replicas: &one,
			Selector: &metav1.LabelSelector{MatchLabels: lbl},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: lbl}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "old"}}}}}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "legacy", Name: "unrelated"}, Spec: appsv1.DeploymentSpec{Replicas: &one,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "cache"}},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "cache"}}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "redis"}}}}}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: "legacy", Name: "legacy"}, Spec: corev1.ServiceSpec{Selector: lbl, Ports: []corev1.ServicePort{{Port: 80}}}},
		&networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Namespace: "legacy", Name: "legacy"}, Spec: networkingv1.IngressSpec{
			TLS: []networkingv1.IngressTLS{{Hosts: []string{"legacy.joserod.space"}, SecretName: "legacy-tls"}},
			Rules: []networkingv1.IngressRule{{Host: "legacy.joserod.space", IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
				Paths: []networkingv1.HTTPIngressPath{{Path: "/", PathType: &pt, Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{
					Name: "legacy", Port: networkingv1.ServiceBackendPort{Number: 80}}}}}}}}}}},
	} {
		if err := c.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}

	app := &v1alpha1.App{ObjectMeta: metav1.ObjectMeta{Name: "legacy"}, Spec: v1alpha1.AppSpec{
		Services: []spec.Service{{Name: "web", Path: ".", Port: 3000, Size: spec.SizeSmall, Replicas: 1, Domain: "legacy.joserod.space",
			TLSSecret: "legacy-tls", Build: spec.Build{Dockerfile: "Dockerfile"}}},
		Images: map[string]string{"web": "registry.local/legacy-web@sha256:1"},
	}}
	if err := c.Create(ctx, app); err != nil {
		t.Fatal(err)
	}
	get := func() v1alpha1.App {
		var a v1alpha1.App
		_ = c.Get(ctx, client.ObjectKey{Name: "legacy"}, &a)
		return a
	}
	// Without consent, an existing namespace is never taken.
	eventually(t, "blocked without adopt", func() bool {
		a := get()
		return a.Status.Phase == v1alpha1.PhaseError && strings.Contains(a.Status.Message, "migrate it (adopt)")
	})

	updateApp(t, c, "legacy", func(a *v1alpha1.App) { a.Spec.Adopt = true })
	key := func(n string) client.ObjectKey { return client.ObjectKey{Namespace: "legacy", Name: n} }
	var web appsv1.Deployment
	eventually(t, "new deployment next to the old one", func() bool { return c.Get(ctx, key("web"), &web) == nil })
	eventually(t, "migrating status", func() bool { return strings.Contains(get().Status.Message, "migrating") })
	// Traffic must not move while the new pods are not ready.
	var ing networkingv1.Ingress
	if err := c.Get(ctx, key("legacy"), &ing); err != nil {
		t.Fatalf("legacy ingress removed too early: %v", err)
	}
	if err := c.Get(ctx, key("web"), &ing); !apierrors.IsNotFound(err) {
		t.Fatalf("new ingress created before pods were ready: %v", err)
	}

	// The new pods become ready (envtest has no kubelet, so set status).
	web.Status = appsv1.DeploymentStatus{ObservedGeneration: web.Generation, Replicas: 1, UpdatedReplicas: 1, ReadyReplicas: 1, AvailableReplicas: 1}
	if err := c.Status().Update(ctx, &web); err != nil {
		t.Fatal(err)
	}

	eventually(t, "traffic switched to the new ingress", func() bool {
		return c.Get(ctx, key("web"), &ing) == nil && apierrors.IsNotFound(c.Get(ctx, key("legacy"), &networkingv1.Ingress{}))
	})
	if ing.Spec.TLS[0].SecretName != "legacy-tls" || ing.Spec.Rules[0].Host != "legacy.joserod.space" {
		t.Fatalf("new ingress = %+v", ing.Spec)
	}
	eventually(t, "legacy workload removed", func() bool {
		return apierrors.IsNotFound(c.Get(ctx, key("legacy"), &appsv1.Deployment{})) &&
			apierrors.IsNotFound(c.Get(ctx, key("legacy"), &corev1.Service{}))
	})
	if err := c.Get(ctx, key("unrelated"), &appsv1.Deployment{}); err != nil {
		t.Fatalf("unrelated deployment must survive: %v", err)
	}
	if err := c.Get(ctx, key("legacy-practice"), &corev1.Secret{}); err != nil {
		t.Fatalf("existing secret must survive: %v", err)
	}
	var ns corev1.Namespace
	_ = c.Get(ctx, client.ObjectKey{Name: "legacy"}, &ns)
	if ns.Labels[render.LabelApp] != "legacy" {
		t.Fatalf("namespace not labeled as adopted: %v", ns.Labels)
	}
	eventually(t, "no longer migrating", func() bool { return !strings.Contains(get().Status.Message, "migrating") })

	// cert-manager names the Certificate after the TLS secret; readiness must
	// follow the tlsSecret override, not <service>-tls.
	cert := &unstructured.Unstructured{}
	cert.SetAPIVersion("cert-manager.io/v1")
	cert.SetKind("Certificate")
	cert.SetNamespace("legacy")
	cert.SetName("legacy-tls")
	_ = unstructured.SetNestedSlice(cert.Object, []any{map[string]any{"type": "Ready", "status": "True"}}, "status", "conditions")
	if err := c.Create(ctx, cert); err != nil {
		t.Fatal(err)
	}
	updateApp(t, c, "legacy", func(a *v1alpha1.App) { a.Spec.Release = 2 }) // any spec change triggers a reconcile
	eventually(t, "cert ready via tlsSecret", func() bool {
		s := get().Status.Services
		return len(s) == 1 && s[0].CertReady
	})
}

// Adoption only covers the app's own namespace: a host served elsewhere still blocks.
func TestAdoptStillBlocksOtherNamespaces(t *testing.T) {
	c, _ := setup(t)
	ctx := context.Background()
	pt := networkingv1.PathTypePrefix
	_ = c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "elsewhere"}})
	_ = c.Create(ctx, &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Namespace: "elsewhere", Name: "x"}, Spec: networkingv1.IngressSpec{
		Rules: []networkingv1.IngressRule{{Host: "taken.joserod.space", IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
			Paths: []networkingv1.HTTPIngressPath{{Path: "/", PathType: &pt, Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{
				Name: "x", Port: networkingv1.ServiceBackendPort{Number: 80}}}}}}}}}}})
	app := &v1alpha1.App{ObjectMeta: metav1.ObjectMeta{Name: "grabby"}, Spec: v1alpha1.AppSpec{Adopt: true,
		Services: []spec.Service{{Name: "web", Path: ".", Port: 80, Size: spec.SizeSmall, Replicas: 1, Domain: "taken.joserod.space", Build: spec.Build{Dockerfile: "Dockerfile"}}},
		Images:   map[string]string{"web": "x@sha256:1"}}}
	if err := c.Create(ctx, app); err != nil {
		t.Fatal(err)
	}
	eventually(t, "blocked", func() bool {
		var a v1alpha1.App
		_ = c.Get(ctx, client.ObjectKey{Name: "grabby"}, &a)
		return a.Status.Phase == v1alpha1.PhaseError && strings.Contains(a.Status.Message, "elsewhere/x")
	})
}

// In-place adoption (simplerfc): same-named Deployment, Service and Ingress
// deployed by ArgoCD are taken over without being recreated; the existing
// volume claim is reused and nothing the old owner set survives.
func TestAdoptInPlace(t *testing.T) {
	c, _ := setup(t)
	ctx := context.Background()
	one := int32(1)
	pt := networkingv1.PathTypePrefix
	lbl := map[string]string{"app": "rfc"}
	argo := map[string]string{"argocd.argoproj.io/tracking-id": "rfc:apps/Deployment:rfc/rfc"}
	for _, o := range []client.Object{
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "rfc"}},
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Namespace: "rfc", Name: "rfc-data-pvc"}, Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources:   corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("5Gi")}}}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "rfc", Name: "rfc", Labels: lbl, Annotations: argo}, Spec: appsv1.DeploymentSpec{Replicas: &one,
			Selector: &metav1.LabelSelector{MatchLabels: lbl}, Strategy: appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: lbl}, Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name: "legacy-name", Image: "registry.cube.local:5000/rfc:1.0.2", Env: []corev1.EnvVar{{Name: "LEGACY", Value: "1"}}}}}}}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: "rfc", Name: "rfc", Annotations: argo}, Spec: corev1.ServiceSpec{Selector: lbl,
			Ports: []corev1.ServicePort{{Port: 80, TargetPort: intstr.FromInt(3000)}}}},
		&networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Namespace: "rfc", Name: "rfc", Annotations: argo}, Spec: networkingv1.IngressSpec{
			TLS: []networkingv1.IngressTLS{{Hosts: []string{"rfc.joserod.space"}, SecretName: "rfc-tls"}},
			Rules: []networkingv1.IngressRule{{Host: "rfc.joserod.space", IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
				Paths: []networkingv1.HTTPIngressPath{{Path: "/", PathType: &pt, Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{
					Name: "rfc", Port: networkingv1.ServiceBackendPort{Number: 80}}}}}}}}}}},
	} {
		if err := c.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	key := client.ObjectKey{Namespace: "rfc", Name: "rfc"}
	var oldSvc corev1.Service
	var oldIng networkingv1.Ingress
	_ = c.Get(ctx, key, &oldSvc)
	_ = c.Get(ctx, key, &oldIng)

	app := &v1alpha1.App{ObjectMeta: metav1.ObjectMeta{Name: "rfc"}, Spec: v1alpha1.AppSpec{Adopt: true,
		Services: []spec.Service{{Name: "rfc", Path: ".", Port: 3000, Size: spec.SizeLarge, Replicas: 1, Domain: "rfc.joserod.space",
			Env: map[string]string{"DB_PATH": "/data/rfc.db"}, Volume: &spec.Volume{ExistingClaim: "rfc-data-pvc", Mount: "/data"},
			Build: spec.Build{Dockerfile: "Dockerfile"}}},
		Images: map[string]string{"rfc": "registry.local/rfc-rfc@sha256:1"},
	}}
	if err := c.Create(ctx, app); err != nil {
		t.Fatal(err)
	}

	var d appsv1.Deployment
	eventually(t, "deployment taken over", func() bool {
		return c.Get(ctx, key, &d) == nil && d.Labels[render.LabelManagedBy] == render.ManagedBy
	})
	ct := d.Spec.Template.Spec.Containers
	if len(ct) != 1 || ct[0].Name != "rfc" || ct[0].Image != "registry.local/rfc-rfc@sha256:1" {
		t.Fatalf("containers = %+v", ct)
	}
	for _, e := range ct[0].Env {
		if e.Name == "LEGACY" {
			t.Fatal("legacy env survived the takeover")
		}
	}
	if d.Spec.Selector.MatchLabels["app"] != "rfc" || len(d.Spec.Selector.MatchLabels) != 1 || d.Spec.Template.Labels[render.LabelApp] != "rfc" {
		t.Fatalf("selector %v, pod labels %v", d.Spec.Selector.MatchLabels, d.Spec.Template.Labels)
	}
	if d.Annotations["argocd.argoproj.io/tracking-id"] != "" || len(d.OwnerReferences) != 1 {
		t.Fatalf("annotations %v owners %v", d.Annotations, d.OwnerReferences)
	}
	if v := d.Spec.Template.Spec.Volumes; len(v) != 1 || v[0].PersistentVolumeClaim.ClaimName != "rfc-data-pvc" || d.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
		t.Fatalf("volumes %+v strategy %s", v, d.Spec.Strategy.Type)
	}

	var svc corev1.Service
	eventually(t, "service taken over", func() bool {
		return c.Get(ctx, key, &svc) == nil && svc.Labels[render.LabelManagedBy] == render.ManagedBy
	})
	if svc.Spec.ClusterIP != oldSvc.Spec.ClusterIP || svc.Spec.Selector["app"] != "rfc" || len(svc.Spec.Ports) != 2 || svc.Annotations["argocd.argoproj.io/tracking-id"] != "" {
		t.Fatalf("service = %+v", svc.Spec)
	}

	var ing networkingv1.Ingress
	eventually(t, "ingress taken over in place", func() bool {
		return c.Get(ctx, key, &ing) == nil && ing.Labels[render.LabelManagedBy] == render.ManagedBy
	})
	if ing.UID != oldIng.UID {
		t.Fatal("ingress was recreated instead of taken over (would cause a gap)")
	}
	if ing.Annotations["argocd.argoproj.io/tracking-id"] != "" || ing.Annotations["cert-manager.io/cluster-issuer"] != "letsencrypt-prod" || ing.Spec.TLS[0].SecretName != "rfc-tls" {
		t.Fatalf("ingress = %+v %+v", ing.Annotations, ing.Spec.TLS)
	}

	var pvcs corev1.PersistentVolumeClaimList
	_ = c.List(ctx, &pvcs, client.InNamespace("rfc"))
	if len(pvcs.Items) != 1 {
		t.Fatalf("expected only the existing claim, got %d PVCs", len(pvcs.Items))
	}

	_ = c.Get(ctx, key, &d)
	for _, mf := range d.ManagedFields {
		if mf.Manager != "rendimiento" || mf.Operation != metav1.ManagedFieldsOperationApply {
			if mf.Manager == "kube-controller-manager" || strings.HasSuffix(mf.Subresource, "status") {
				continue // status is written by the deployment controller
			}
			t.Fatalf("unexpected field owner after takeover: %s/%s", mf.Manager, mf.Operation)
		}
	}
	// After the takeover rendimiento fully owns the fields: removing a
	// variable from rendimiento.yaml removes it from the Deployment.
	updateApp(t, c, "rfc", func(a *v1alpha1.App) { a.Spec.Services[0].Env = nil })
	eventually(t, "env removal applied", func() bool {
		_ = c.Get(ctx, key, &d)
		for _, e := range d.Spec.Template.Spec.Containers[0].Env {
			if e.Name == "DB_PATH" {
				return false
			}
		}
		return true
	})
}

// Scheduled jobs: an existing CronJob (from ArgoCD) is taken over in place,
// and a job removed from rendimiento.yaml is pruned.
func TestJobsAdoptAndPrune(t *testing.T) {
	c, _ := setup(t)
	ctx := context.Background()
	_ = c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "well"}})
	legacy := &batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Namespace: "well", Name: "recap",
		Annotations: map[string]string{"argocd.argoproj.io/tracking-id": "well:batch/CronJob:well/recap"}},
		Spec: batchv1.CronJobSpec{Schedule: "0 9 * * *", JobTemplate: batchv1.JobTemplateSpec{Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, Containers: []corev1.Container{{Name: "old", Image: "registry.cube.local:5000/wellness-api:1.5.8"}}}}}}}}
	if err := c.Create(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	app := &v1alpha1.App{ObjectMeta: metav1.ObjectMeta{Name: "well"}, Spec: v1alpha1.AppSpec{Adopt: true,
		Services: []spec.Service{{Name: "api", Path: ".", Port: 8000, Size: spec.SizeSmall, Replicas: 1, Build: spec.Build{Dockerfile: "Dockerfile"}}},
		Jobs: []spec.Job{
			{Name: "recap", Schedule: "0 9 * * MON", TimeZone: "America/New_York", Service: "api", Size: spec.SizeSmall, Command: []string{"python", "-c", "recap()"}},
			{Name: "ping", Schedule: "@hourly", Image: "curlimages/curl:8.10.1", Size: spec.SizeSmall},
		},
		Images: map[string]string{"api": "registry.local/well-api@sha256:1"},
	}}
	if err := c.Create(ctx, app); err != nil {
		t.Fatal(err)
	}
	key := client.ObjectKey{Namespace: "well", Name: "recap"}
	var cj batchv1.CronJob
	eventually(t, "cronjob taken over", func() bool {
		return c.Get(ctx, key, &cj) == nil && cj.Labels[render.LabelManagedBy] == render.ManagedBy
	})
	ct := cj.Spec.JobTemplate.Spec.Template.Spec.Containers
	if cj.UID != legacy.UID || cj.Spec.Schedule != "0 9 * * MON" || *cj.Spec.TimeZone != "America/New_York" ||
		len(ct) != 1 || ct[0].Image != "registry.local/well-api@sha256:1" || cj.Annotations["argocd.argoproj.io/tracking-id"] != "" {
		t.Fatalf("cronjob = %+v containers %+v", cj.Spec, ct)
	}
	eventually(t, "new cronjob", func() bool {
		return c.Get(ctx, client.ObjectKey{Namespace: "well", Name: "ping"}, &batchv1.CronJob{}) == nil
	})

	updateApp(t, c, "well", func(a *v1alpha1.App) { a.Spec.Jobs = a.Spec.Jobs[:1] })
	eventually(t, "removed job pruned", func() bool {
		return apierrors.IsNotFound(c.Get(ctx, client.ObjectKey{Namespace: "well", Name: "ping"}, &batchv1.CronJob{}))
	})
}

// stockpulse's ingress: a differently named ingress kept by name, several
// hosts on two certificates, extra nginx settings; aliases get DNS and
// count for conflicts.
func TestAdoptRenamedIngressWithAliases(t *testing.T) {
	c, dns := setup(t)
	ctx := context.Background()
	pt := networkingv1.PathTypePrefix
	backend := networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{Paths: []networkingv1.HTTPIngressPath{{
		Path: "/", PathType: &pt, Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: "ui", Port: networkingv1.ServiceBackendPort{Number: 80}}}}}}}
	_ = c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "sp"}})
	legacy := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Namespace: "sp", Name: "sp-ingress"}, Spec: networkingv1.IngressSpec{
		TLS:   []networkingv1.IngressTLS{{Hosts: []string{"sp.joserod.space"}, SecretName: "sp-tls"}, {Hosts: []string{"sf.com", "www.sf.com"}, SecretName: "sf-tls"}},
		Rules: []networkingv1.IngressRule{{Host: "sp.joserod.space", IngressRuleValue: backend}, {Host: "sf.com", IngressRuleValue: backend}, {Host: "www.sf.com", IngressRuleValue: backend}}}}
	if err := c.Create(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	app := &v1alpha1.App{ObjectMeta: metav1.ObjectMeta{Name: "sp"}, Spec: v1alpha1.AppSpec{Adopt: true,
		Services: []spec.Service{{Name: "ui", Path: ".", Port: 80, Size: spec.SizeSmall, Replicas: 1, Build: spec.Build{Dockerfile: "Dockerfile"},
			Domain: "sp.joserod.space", Aliases: []string{"sf.com", "www.sf.com"}, TLSSecret: "sp-tls",
			Ingress: &spec.IngressOptions{Name: "sp-ingress", TLSSecrets: map[string]string{"sf.com": "sf-tls", "www.sf.com": "sf-tls"},
				Annotations: map[string]string{"nginx.ingress.kubernetes.io/limit-rps": "5"}}}},
		Images: map[string]string{"ui": "registry.local/sp-ui@sha256:1"}}}
	if err := c.Create(ctx, app); err != nil {
		t.Fatal(err)
	}
	var ing networkingv1.Ingress
	key := client.ObjectKey{Namespace: "sp", Name: "sp-ingress"}
	eventually(t, "renamed ingress taken over in place", func() bool {
		return c.Get(ctx, key, &ing) == nil && ing.Labels[render.LabelManagedBy] == render.ManagedBy
	})
	if ing.UID != legacy.UID || len(ing.Spec.TLS) != 2 || ing.Spec.TLS[1].SecretName != "sf-tls" || ing.Annotations["nginx.ingress.kubernetes.io/limit-rps"] != "5" {
		t.Fatalf("ingress = %+v %v", ing.Spec.TLS, ing.Annotations)
	}
	if err := c.Get(ctx, client.ObjectKey{Namespace: "sp", Name: "ui"}, &networkingv1.Ingress{}); !apierrors.IsNotFound(err) {
		t.Fatal("no second ingress named after the service may be created")
	}
	eventually(t, "DNS for every host", func() bool { return dns.has("sp.joserod.space") && dns.has("sf.com") && dns.has("www.sf.com") })

	// An alias already served by another app blocks, like a main domain would.
	_ = c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "other"}})
	_ = c.Create(ctx, &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Namespace: "other", Name: "x"}, Spec: networkingv1.IngressSpec{
		Rules: []networkingv1.IngressRule{{Host: "shop.sf2.com", IngressRuleValue: backend}}}})
	clash := &v1alpha1.App{ObjectMeta: metav1.ObjectMeta{Name: "clash"}, Spec: v1alpha1.AppSpec{
		Services: []spec.Service{{Name: "web", Path: ".", Port: 80, Size: spec.SizeSmall, Replicas: 1, Build: spec.Build{Dockerfile: "Dockerfile"},
			Domain: "new.sf2.com", Aliases: []string{"shop.sf2.com"}}},
		Images: map[string]string{"web": "x@sha256:1"}}}
	if err := c.Create(ctx, clash); err != nil {
		t.Fatal(err)
	}
	eventually(t, "alias clash blocked", func() bool {
		var a v1alpha1.App
		_ = c.Get(ctx, client.ObjectKey{Name: "clash"}, &a)
		return a.Status.Phase == v1alpha1.PhaseError && strings.Contains(a.Status.Message, "shop.sf2.com")
	})
}

// updateApp changes an App the way a user would, retrying when the
// controller wrote it in between (optimistic concurrency).
func updateApp(t *testing.T, c client.Client, name string, mutate func(*v1alpha1.App)) {
	t.Helper()
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var a v1alpha1.App
		if err := c.Get(context.Background(), client.ObjectKey{Name: name}, &a); err != nil {
			return err
		}
		mutate(&a)
		return c.Update(context.Background(), &a)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// wellness: /api moves from the website's ingress to the API's ingress; both
// legacy ingresses are taken over in place under their existing names.
func TestAdoptWithPathRoutes(t *testing.T) {
	c, _ := setup(t)
	ctx := context.Background()
	pt := networkingv1.PathTypePrefix
	path := func(p, svc string) networkingv1.HTTPIngressPath {
		return networkingv1.HTTPIngressPath{Path: p, PathType: &pt, Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{
			Name: svc, Port: networkingv1.ServiceBackendPort{Number: 80}}}}
	}
	rule := func(host string, paths ...networkingv1.HTTPIngressPath) networkingv1.IngressRule {
		return networkingv1.IngressRule{Host: host, IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{Paths: paths}}}
	}
	_ = c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "well"}})
	site := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Namespace: "well", Name: "wellness-ingress"}, Spec: networkingv1.IngressSpec{
		Rules: []networkingv1.IngressRule{rule("w.example.com", path("/api", "api"), path("/", "ui"))}}}
	apiIng := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Namespace: "well", Name: "wellness-api-ingress"}, Spec: networkingv1.IngressSpec{
		Rules: []networkingv1.IngressRule{rule("api.w.example.com", path("/", "api"))}}}
	for _, o := range []client.Object{site, apiIng} {
		if err := c.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	svc := func(name, domain string, routes []string, ing string) spec.Service {
		return spec.Service{Name: name, Path: name, Port: 8000, Size: spec.SizeSmall, Replicas: 1, Build: spec.Build{Dockerfile: "Dockerfile"},
			Domain: domain, Routes: routes, Ingress: &spec.IngressOptions{Name: ing}}
	}
	app := &v1alpha1.App{ObjectMeta: metav1.ObjectMeta{Name: "well"}, Spec: v1alpha1.AppSpec{Adopt: true,
		// The API is listed first on purpose: ordering must not matter.
		Services: []spec.Service{
			svc("api", "api.w.example.com", []string{"w.example.com/api"}, "wellness-api-ingress"),
			svc("ui", "w.example.com", nil, "wellness-ingress"),
		},
		Images: map[string]string{"api": "a@sha256:1", "ui": "u@sha256:1"}}}
	if err := c.Create(ctx, app); err != nil {
		t.Fatal(err)
	}
	routes := func(name string) string {
		var ing networkingv1.Ingress
		if c.Get(ctx, client.ObjectKey{Namespace: "well", Name: name}, &ing) != nil || ing.Labels[render.LabelManagedBy] != render.ManagedBy {
			return ""
		}
		var out []string
		for _, r := range ing.Spec.Rules {
			for _, p := range r.HTTP.Paths {
				out = append(out, r.Host+p.Path+"->"+p.Backend.Service.Name)
			}
		}
		return strings.Join(out, " ")
	}
	eventually(t, "both ingresses taken over with /api moved", func() bool {
		return routes("wellness-ingress") == "w.example.com/->ui" &&
			routes("wellness-api-ingress") == "api.w.example.com/->api w.example.com/api->api"
	})
	var got networkingv1.Ingress
	_ = c.Get(ctx, client.ObjectKey{Namespace: "well", Name: "wellness-ingress"}, &got)
	if got.UID != site.UID {
		t.Fatal("website ingress was recreated instead of taken over")
	}
}

// Disconnecting keeps the app's DNS records: its workloads keep serving.
func TestDisconnectKeepsDNS(t *testing.T) {
	c, dns := setup(t)
	ctx := context.Background()
	app := &v1alpha1.App{ObjectMeta: metav1.ObjectMeta{Name: "keep"}, Spec: v1alpha1.AppSpec{
		Services: []spec.Service{{Name: "web", Path: ".", Port: 80, Size: spec.SizeSmall, Replicas: 1, Build: spec.Build{Dockerfile: "Dockerfile"}, Domain: "keep.joserod.space"}},
		Images:   map[string]string{"web": "x@sha256:1"}}}
	if err := c.Create(ctx, app); err != nil {
		t.Fatal(err)
	}
	eventually(t, "DNS record", func() bool { return dns.has("keep.joserod.space") })
	updateApp(t, c, "keep", func(a *v1alpha1.App) {
		a.Annotations = map[string]string{AnnotationDisconnect: "true"}
		a.Spec.Suspend = true
	})
	if err := c.Delete(ctx, &v1alpha1.App{ObjectMeta: metav1.ObjectMeta{Name: "keep"}}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "app gone", func() bool {
		return apierrors.IsNotFound(c.Get(ctx, client.ObjectKey{Name: "keep"}, &v1alpha1.App{}))
	})
	if !dns.has("keep.joserod.space") {
		t.Fatal("disconnect must keep the DNS record")
	}
}

// A rollout where the new pod crash-loops while an old pod keeps serving
// must not be reported Healthy (wellness-api with a broken image).
func TestStuckRolloutIsNotHealthy(t *testing.T) {
	c, _ := setup(t)
	ctx := context.Background()
	app := &v1alpha1.App{ObjectMeta: metav1.ObjectMeta{Name: "stuck"}, Spec: v1alpha1.AppSpec{
		Services: []spec.Service{{Name: "api", Path: ".", Port: 8000, Size: spec.SizeSmall, Replicas: 1, Build: spec.Build{Dockerfile: "Dockerfile"}}},
		Images:   map[string]string{"api": "x@sha256:2"}}}
	if err := c.Create(ctx, app); err != nil {
		t.Fatal(err)
	}
	key := client.ObjectKey{Namespace: "stuck", Name: "api"}
	var d appsv1.Deployment
	eventually(t, "deployment", func() bool { return c.Get(ctx, key, &d) == nil })
	// Old pod ready and serving, new pod not: 2 pods, 1 updated, 1 ready.
	d.Status = appsv1.DeploymentStatus{ObservedGeneration: d.Generation, Replicas: 2, UpdatedReplicas: 1, ReadyReplicas: 1, AvailableReplicas: 1}
	if err := c.Status().Update(ctx, &d); err != nil {
		t.Fatal(err)
	}
	get := func() v1alpha1.App {
		var a v1alpha1.App
		_ = c.Get(ctx, client.ObjectKey{Name: "stuck"}, &a)
		return a
	}
	updateApp(t, c, "stuck", func(a *v1alpha1.App) { a.Spec.Release = 1 }) // trigger a reconcile
	eventually(t, "progressing while an old pod still serves", func() bool {
		a := get()
		return a.Status.Phase == v1alpha1.PhaseProgressing && len(a.Status.Services) == 1 && strings.Contains(a.Status.Services[0].Message, "old pod")
	})

	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "stuck", Name: "api-new", Labels: d.Spec.Selector.MatchLabels},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "api", Image: "x"}}}}
	if err := c.Create(ctx, pod); err != nil {
		t.Fatal(err)
	}
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "api", RestartCount: 7,
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}}}
	if err := c.Status().Update(ctx, pod); err != nil {
		t.Fatal(err)
	}
	updateApp(t, c, "stuck", func(a *v1alpha1.App) { a.Spec.Release = 2 })
	eventually(t, "degraded with the crash loop named", func() bool {
		a := get()
		return a.Status.Phase == v1alpha1.PhaseDegraded && strings.Contains(a.Status.Services[0].Message, "api-new: CrashLoopBackOff (7 restarts)")
	})
}

// jobsentry: the namespace belongs to ArgoCD (linguistic-ai stays there);
// rendimiento runs its services in it without touching the namespace.
func TestSharedNamespace(t *testing.T) {
	c, _ := setup(t)
	ctx := context.Background()
	app := func(name string) *v1alpha1.App {
		return &v1alpha1.App{ObjectMeta: metav1.ObjectMeta{Name: name}, Spec: v1alpha1.AppSpec{Adopt: true, SharedNamespace: true,
			Services: []spec.Service{{Name: "web", Path: ".", Port: 80, Size: spec.SizeSmall, Replicas: 1, Build: spec.Build{Dockerfile: "Dockerfile"}}},
			Images:   map[string]string{"web": "x@sha256:1"}}}
	}
	// A shared namespace is never created.
	if err := c.Create(ctx, app("nowhere")); err != nil {
		t.Fatal(err)
	}
	eventually(t, "missing shared namespace blocks", func() bool {
		var a v1alpha1.App
		_ = c.Get(ctx, client.ObjectKey{Name: "nowhere"}, &a)
		return a.Status.Phase == v1alpha1.PhaseError && strings.Contains(a.Status.Message, "must exist")
	})
	if err := c.Get(ctx, client.ObjectKey{Name: "nowhere"}, &corev1.Namespace{}); !apierrors.IsNotFound(err) {
		t.Fatal("shared namespace must not be created")
	}

	argoLabels := map[string]string{"app.kubernetes.io/name": "jobsentry-linguistic"}
	if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "js", Labels: argoLabels,
		Annotations: map[string]string{"argocd.argoproj.io/tracking-id": "linguistic:/Namespace:js/js"}}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Create(ctx, app("js")); err != nil {
		t.Fatal(err)
	}
	eventually(t, "service deployed in the shared namespace", func() bool {
		return c.Get(ctx, client.ObjectKey{Namespace: "js", Name: "web"}, &appsv1.Deployment{}) == nil
	})
	var ns corev1.Namespace
	_ = c.Get(ctx, client.ObjectKey{Name: "js"}, &ns)
	if len(ns.OwnerReferences) != 0 || ns.Labels[render.LabelManagedBy] != "" || ns.Labels[render.LabelApp] != "" ||
		ns.Annotations["argocd.argoproj.io/tracking-id"] == "" || ns.Labels["app.kubernetes.io/name"] != "jobsentry-linguistic" {
		t.Fatalf("shared namespace was modified: owners=%v labels=%v", ns.OwnerReferences, ns.Labels)
	}
}

func TestAppNeeds(t *testing.T) {
	c, _ := setup(t)
	ctx := context.Background()
	// Another app's service to call.
	if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ai"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Create(ctx, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: "ai", Name: "ollama"},
		Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: "http", Port: 80, TargetPort: intstr.FromInt32(11434)}, {Name: "app", Port: 11434}}}}); err != nil {
		t.Fatal(err)
	}
	app := &v1alpha1.App{ObjectMeta: metav1.ObjectMeta{Name: "shop"}, Spec: v1alpha1.AppSpec{
		Images: map[string]string{"api": "reg/api@sha256:1"},
		Services: []spec.Service{{Name: "api", Path: ".", Port: 8080, Size: spec.SizeSmall, Replicas: 1,
			Needs: []spec.Need{{Kind: spec.NeedPostgres}, {Kind: spec.NeedRedis}, {Kind: spec.NeedService, Service: "ai/ollama", Env: "OLLAMA_HOST"}}}},
	}}
	if err := c.Create(ctx, app); err != nil {
		t.Fatal(err)
	}
	var pg corev1.Secret
	eventually(t, "postgres credentials", func() bool {
		return c.Get(ctx, client.ObjectKey{Namespace: "shop", Name: render.PostgresSecret}, &pg) == nil
	})
	uri := string(pg.Data["uri"])
	if !strings.HasPrefix(uri, "postgresql://app:") || !strings.HasSuffix(uri, "@postgres:5432/shop?sslmode=disable") || len(pg.Data["password"]) < 32 {
		t.Fatalf("uri = %s", uri)
	}
	eventually(t, "database, cache and api deployed", func() bool {
		for _, n := range []string{"postgres", "redis", "api"} {
			if c.Get(ctx, client.ObjectKey{Namespace: "shop", Name: n}, &appsv1.Deployment{}) != nil {
				return false
			}
		}
		return true
	})
	var api appsv1.Deployment
	_ = c.Get(ctx, client.ObjectKey{Namespace: "shop", Name: "api"}, &api)
	env := map[string]corev1.EnvVar{}
	for _, e := range api.Spec.Template.Spec.Containers[0].Env {
		env[e.Name] = e
	}
	if e := env["DATABASE_URL"]; e.ValueFrom == nil || e.ValueFrom.SecretKeyRef.Name != render.PostgresSecret {
		t.Errorf("DATABASE_URL = %+v", e)
	}
	if env["OLLAMA_HOST"].Value != "http://ollama.ai.svc.cluster.local:11434" {
		t.Errorf("OLLAMA_HOST = %q (the app port, by cluster DNS name)", env["OLLAMA_HOST"].Value)
	}

	// Credentials are never regenerated, whatever happens later.
	updateApp(t, c, "shop", func(a *v1alpha1.App) { a.Spec.Services[0].Replicas = 2 })
	eventually(t, "api scaled", func() bool {
		_ = c.Get(ctx, client.ObjectKey{Namespace: "shop", Name: "api"}, &api)
		return *api.Spec.Replicas == 2
	})
	var again corev1.Secret
	_ = c.Get(ctx, client.ObjectKey{Namespace: "shop", Name: render.PostgresSecret}, &again)
	if string(again.Data["password"]) != string(pg.Data["password"]) {
		t.Fatal("the database password changed")
	}

	// A service that does not exist is reported, not crashed on.
	updateApp(t, c, "shop", func(a *v1alpha1.App) {
		a.Spec.Services[0].Needs = append(a.Spec.Services[0].Needs, spec.Need{Kind: spec.NeedService, Service: "ai/missing"})
	})
	eventually(t, "missing service reported", func() bool {
		var a v1alpha1.App
		_ = c.Get(ctx, client.ObjectKey{Name: "shop"}, &a)
		return a.Status.Phase == v1alpha1.PhaseError && strings.Contains(a.Status.Message, "ai/missing")
	})
}

func TestAppLANURL(t *testing.T) {
	c, _ := setup(t)
	ctx := context.Background()
	app := &v1alpha1.App{ObjectMeta: metav1.ObjectMeta{Name: "book"}, Spec: v1alpha1.AppSpec{
		Images: map[string]string{"docs": "reg/docs@sha256:1"},
		Services: []spec.Service{{Name: "docs", Path: ".", Port: 8080, Size: spec.SizeSmall, Replicas: 1,
			LAN: &spec.LANOptions{IP: "192.168.1.81"}}},
	}}
	if err := c.Create(ctx, app); err != nil {
		t.Fatal(err)
	}
	var lan corev1.Service
	key := client.ObjectKey{Namespace: "book", Name: "docs-lan"}
	eventually(t, "LAN service", func() bool { return c.Get(ctx, key, &lan) == nil })
	// envtest has no load balancer: play MetalLB and assign the address.
	lan.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{IP: "192.168.1.81"}}
	if err := c.Status().Update(ctx, &lan); err != nil {
		t.Fatal(err)
	}
	eventually(t, "LAN URL in status", func() bool {
		var a v1alpha1.App
		if c.Get(ctx, client.ObjectKey{Name: "book"}, &a) != nil {
			return false
		}
		for _, s := range a.Status.Services {
			if s.Name == "docs" && s.LANURL == "http://192.168.1.81" {
				return true
			}
		}
		return false
	})
}
