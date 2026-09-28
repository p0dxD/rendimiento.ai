package controller

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/p0dxD/rendimiento.ai/api/v1alpha1"
	"github.com/p0dxD/rendimiento.ai/internal/addon"
)

// fakeGit serves folders that tests can change between syncs.
type fakeGit struct {
	mu      sync.Mutex
	folders map[string]fstest.MapFS
}

func (f *fakeGit) Fetch(_ context.Context, repo, path, _ string) (fs.FS, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.folders[repo+"/"+path], "sha-" + path, nil
}

func (f *fakeGit) set(path string, files map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := fstest.MapFS{}
	for name, data := range files {
		m[name] = &fstest.MapFile{Data: []byte(data)}
	}
	f.folders["o/gitops/"+path] = m
}

func setupAddons(t *testing.T) (client.Client, *fakeGit) {
	t.Helper()
	ctrl.SetLogger(logr.Discard())
	env := &envtest.Environment{CRDDirectoryPaths: []string{filepath.Join("..", "..", "deploy", "crds")}, ErrorIfCRDPathMissing: true}
	cfg, err := env.Start()
	if err != nil {
		if os.Getenv("KUBEBUILDER_ASSETS") != "" {
			t.Fatalf("envtest failed to start: %v", err)
		}
		t.Skipf("envtest unavailable: %v", err)
	}
	t.Cleanup(func() { _ = env.Stop() })
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{Scheme: scheme, Metrics: metricsserver.Options{BindAddress: "0"}, Controller: config.Controller{SkipNameValidation: &skip}})
	if err != nil {
		t.Fatal(err)
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	git := &fakeGit{folders: map[string]fstest.MapFS{}}
	if err := (&AddonReconciler{Client: mgr.GetClient(), Target: c, Renderer: &addon.Renderer{Git: git}}).SetupWithManager(mgr); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = mgr.Start(ctx) }()
	return c, git
}

func cm(name, value string) string {
	return "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: " + name + "}\ndata: {v: \"" + value + "\"}\n"
}

func addonPhase(t *testing.T, c client.Client, name string) (v1alpha1.AddonPhase, *v1alpha1.Addon) {
	t.Helper()
	var a v1alpha1.Addon
	if err := c.Get(context.Background(), client.ObjectKey{Name: name}, &a); err != nil {
		t.Fatal(err)
	}
	return a.Status.Phase, &a
}

func updateAddon(t *testing.T, c client.Client, name string, mutate func(*v1alpha1.Addon)) {
	t.Helper()
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var a v1alpha1.Addon
		if err := c.Get(context.Background(), client.ObjectKey{Name: name}, &a); err != nil {
			return err
		}
		mutate(&a)
		return c.Update(context.Background(), &a)
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAddonLifecycle(t *testing.T) {
	c, git := setupAddons(t)
	ctx := context.Background()
	getCM := func(ns, name string) (*corev1.ConfigMap, error) {
		var x corev1.ConfigMap
		return &x, c.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &x)
	}

	// 1. Install from git: namespace created, objects applied and labelled.
	git.set("tools", map[string]string{
		"kustomization.yaml": "resources: [a.yaml, b.yaml, pvc.yaml]\n",
		"a.yaml":             cm("a", "1"),
		"b.yaml":             cm("b", "1"),
		"pvc.yaml":           "apiVersion: v1\nkind: PersistentVolumeClaim\nmetadata: {name: data}\nspec: {accessModes: [ReadWriteOnce], resources: {requests: {storage: 1Gi}}}\n",
	})
	a := &v1alpha1.Addon{ObjectMeta: metav1.ObjectMeta{Name: "tools"}, Spec: v1alpha1.AddonSpec{
		Namespace: "tools", CreateNamespace: true, Prune: true,
		Source: v1alpha1.AddonSource{Git: &v1alpha1.GitSource{Repo: "o/gitops", Path: "tools"}},
	}}
	if err := c.Create(ctx, a); err != nil {
		t.Fatal(err)
	}
	eventually(t, "tools synced", func() bool { p, _ := addonPhase(t, c, "tools"); return p == v1alpha1.AddonSynced })
	x, err := getCM("tools", "a")
	if err != nil || x.Labels[LabelAddon] != "tools" {
		t.Fatalf("configmap a: %v %v", err, x.Labels)
	}
	_, got := addonPhase(t, c, "tools")
	if len(got.Status.Objects) != 4 || got.Status.Revision != "sha-tools" {
		t.Fatalf("inventory %v revision %s", got.Status.Objects, got.Status.Revision)
	}

	// 2. A source change drops b and the PVC: b is pruned, the PVC never is.
	git.set("tools", map[string]string{"kustomization.yaml": "resources: [a.yaml]\n", "a.yaml": cm("a", "2")})
	updateAddon(t, c, "tools", func(a *v1alpha1.Addon) { a.Spec.Source.Git.Revision = "next" })
	eventually(t, "b pruned", func() bool { _, err := getCM("tools", "b"); return apierrors.IsNotFound(err) })
	if x, _ := getCM("tools", "a"); x.Data["v"] != "2" {
		t.Errorf("a not updated: %v", x.Data)
	}
	var pvc corev1.PersistentVolumeClaim
	if err := c.Get(ctx, client.ObjectKey{Namespace: "tools", Name: "data"}, &pvc); err != nil || !pvc.DeletionTimestamp.IsZero() {
		t.Fatalf("volumes are never pruned: %v", err)
	}

	// 3. Drift is healed on the next sync (triggered here by an annotation change).
	x, _ = getCM("tools", "a")
	x.Data["v"] = "tampered"
	if err := c.Update(ctx, x); err != nil {
		t.Fatal(err)
	}
	updateAddon(t, c, "tools", func(a *v1alpha1.Addon) { a.Annotations = map[string]string{"poke": "1"} })
	eventually(t, "drift healed", func() bool { x, _ := getCM("tools", "a"); return x.Data["v"] == "2" })

	// 3b. The pause annotation stops syncing (drift stays) until removed.
	updateAddon(t, c, "tools", func(a *v1alpha1.Addon) { a.Annotations = map[string]string{AnnotationPaused: "true"} })
	eventually(t, "tools paused", func() bool { p, _ := addonPhase(t, c, "tools"); return p == v1alpha1.AddonSuspended })
	x, _ = getCM("tools", "a")
	x.Data["v"] = "scaled-down"
	if err := c.Update(ctx, x); err != nil {
		t.Fatal(err)
	}
	updateAddon(t, c, "tools", func(a *v1alpha1.Addon) { a.Annotations[AnnotationPaused] = "true"; a.Annotations["poke"] = "2" })
	if x, _ := getCM("tools", "a"); x.Data["v"] != "scaled-down" {
		t.Fatal("a paused add-on must not revert changes")
	}
	updateAddon(t, c, "tools", func(a *v1alpha1.Addon) { delete(a.Annotations, AnnotationPaused) })
	eventually(t, "resumed and healed", func() bool { x, _ := getCM("tools", "a"); return x.Data["v"] == "2" })

	// 4. Adoption: an identical existing object is taken over silently.
	if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "legacy"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "legacy", Name: "same", Annotations: map[string]string{"argocd.argoproj.io/tracking-id": "legacy:/ConfigMap:legacy/same"}}, Data: map[string]string{"v": "1"}}); err != nil {
		t.Fatal(err)
	}
	git.set("legacy", map[string]string{"same.yaml": cm("same", "1")})
	if err := c.Create(ctx, &v1alpha1.Addon{ObjectMeta: metav1.ObjectMeta{Name: "legacy"}, Spec: v1alpha1.AddonSpec{
		Namespace: "legacy", Adopt: true, Source: v1alpha1.AddonSource{Git: &v1alpha1.GitSource{Repo: "o/gitops", Path: "legacy"}},
	}}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "legacy adopted", func() bool { p, _ := addonPhase(t, c, "legacy"); return p == v1alpha1.AddonSynced })
	if x, _ := getCM("legacy", "same"); x.Labels[LabelAddon] != "legacy" {
		t.Error("adopted object is labelled")
	}

	// 5. Adoption that would change a live object is blocked, with a diff.
	if err := c.Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "legacy", Name: "differs"}, Data: map[string]string{"v": "live"}}); err != nil {
		t.Fatal(err)
	}
	git.set("strict", map[string]string{"differs.yaml": cm("differs", "desired")})
	if err := c.Create(ctx, &v1alpha1.Addon{ObjectMeta: metav1.ObjectMeta{Name: "strict"}, Spec: v1alpha1.AddonSpec{
		Namespace: "legacy", Adopt: true, Source: v1alpha1.AddonSource{Git: &v1alpha1.GitSource{Repo: "o/gitops", Path: "strict"}},
	}}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "strict blocked", func() bool { p, _ := addonPhase(t, c, "strict"); return p == v1alpha1.AddonBlocked })
	_, got = addonPhase(t, c, "strict")
	if got.Status.Preview == nil || got.Status.Preview.Update != 1 || !strings.Contains(got.Status.Preview.Items[0].Diff, "desired") {
		t.Fatalf("preview = %+v", got.Status.Preview)
	}
	if x, _ := getCM("legacy", "differs"); x.Data["v"] != "live" {
		t.Fatal("a blocked adoption must not touch the object")
	}
	updateAddon(t, c, "strict", func(a *v1alpha1.Addon) { a.Spec.AllowAdoptChanges = true })
	eventually(t, "strict applied once allowed", func() bool { x, _ := getCM("legacy", "differs"); return x.Data["v"] == "desired" })

	// 6. Manual sync: changes wait until requested.
	git.set("manual", map[string]string{"m.yaml": cm("m", "1")})
	if err := c.Create(ctx, &v1alpha1.Addon{ObjectMeta: metav1.ObjectMeta{Name: "manual"}, Spec: v1alpha1.AddonSpec{
		Namespace: "legacy", ManualSync: true, Source: v1alpha1.AddonSource{Git: &v1alpha1.GitSource{Repo: "o/gitops", Path: "manual"}},
	}}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "manual out of sync", func() bool { p, _ := addonPhase(t, c, "manual"); return p == v1alpha1.AddonOutOfSync })
	if _, err := getCM("legacy", "m"); !apierrors.IsNotFound(err) {
		t.Fatal("manual sync must not apply before it is requested")
	}
	updateAddon(t, c, "manual", func(a *v1alpha1.Addon) { a.Spec.SyncRequest = 1 })
	eventually(t, "manual synced", func() bool { p, _ := addonPhase(t, c, "manual"); return p == v1alpha1.AddonSynced })

	// 7. Deleting an add-on leaves its objects; the uninstall annotation removes them.
	if err := c.Delete(ctx, &v1alpha1.Addon{ObjectMeta: metav1.ObjectMeta{Name: "legacy"}}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "legacy addon gone", func() bool {
		return apierrors.IsNotFound(c.Get(ctx, client.ObjectKey{Name: "legacy"}, &v1alpha1.Addon{}))
	})
	if _, err := getCM("legacy", "same"); err != nil {
		t.Fatal("deleting an add-on must leave what it installed")
	}
	updateAddon(t, c, "tools", func(a *v1alpha1.Addon) { a.Annotations = map[string]string{AnnotationUninstall: "true"} })
	if err := c.Delete(ctx, &v1alpha1.Addon{ObjectMeta: metav1.ObjectMeta{Name: "tools"}}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "tools uninstalled", func() bool { _, err := getCM("tools", "a"); return apierrors.IsNotFound(err) })
	var ns corev1.Namespace
	if err := c.Get(ctx, client.ObjectKey{Name: "tools"}, &ns); err != nil || !ns.DeletionTimestamp.IsZero() {
		t.Fatal("uninstall never deletes namespaces")
	}
}
