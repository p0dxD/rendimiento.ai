package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/p0dxD/rendimiento.ai/api/v1alpha1"
)

// hookChart serves a chart whose install and upgrade run a pre hook Pod
// (replaced each time) and whose upgrade applies a post hook ConfigMap
// that is deleted once it succeeded.
func hookChart(t *testing.T) *httptest.Server {
	t.Helper()
	c := &chart.Chart{
		Metadata: &chart.Metadata{APIVersion: "v2", Name: "hooked", Version: "1.0.0"},
		Values:   map[string]any{"v": "1"},
		Templates: []*chart.File{
			{Name: "templates/cm.yaml", Data: []byte("apiVersion: v1\nkind: ConfigMap\nmetadata: {name: app}\ndata: {v: \"{{ .Values.v }}\"}\n")},
			{Name: "templates/pre.yaml", Data: []byte(`apiVersion: v1
kind: Pod
metadata:
  name: migrate
  annotations: {"helm.sh/hook": "pre-install,pre-upgrade", "helm.sh/hook-weight": "-1"}
spec: {restartPolicy: Never, containers: [{name: m, image: busybox}]}
`)},
			{Name: "templates/post.yaml", Data: []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: post-upgrade-marker
  annotations: {"helm.sh/hook": post-upgrade, "helm.sh/hook-delete-policy": hook-succeeded}
data: {v: "{{ .Values.v }}"}
`)},
		},
	}
	archive, err := chartutil.Save(c, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(archive)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.yaml":
			w.Write([]byte("apiVersion: v1\nentries:\n  hooked:\n    - {apiVersion: v2, name: hooked, version: 1.0.0, urls: [hooked-1.0.0.tgz]}\n"))
		case "/hooked-1.0.0.tgz":
			w.Write(data)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestAddonHooks(t *testing.T) {
	c, _ := setupAddons(t)
	ctx := context.Background()
	srv := hookChart(t)
	defer srv.Close()

	// finishPod plays the kubelet: it marks the hook pod succeeded or failed.
	finishPod := func(phase corev1.PodPhase) {
		t.Helper()
		var pod corev1.Pod
		eventually(t, "hook pod created", func() bool {
			// A new pod starts Pending; one from an earlier run has finished.
			return c.Get(ctx, client.ObjectKey{Namespace: "hooked", Name: "migrate"}, &pod) == nil &&
				(pod.Status.Phase == "" || pod.Status.Phase == corev1.PodPending)
		})
		pod.Status.Phase = phase
		if err := c.Status().Update(ctx, &pod); err != nil {
			t.Fatal(err)
		}
	}
	appValue := func() string {
		var x corev1.ConfigMap
		if err := c.Get(ctx, client.ObjectKey{Namespace: "hooked", Name: "app"}, &x); err != nil {
			return ""
		}
		return x.Data["v"]
	}

	// Install: the pre-install hook runs before anything is applied.
	if err := c.Create(ctx, &v1alpha1.Addon{ObjectMeta: metav1.ObjectMeta{Name: "hooked"}, Spec: v1alpha1.AddonSpec{
		Namespace: "hooked", CreateNamespace: true, Values: "v: \"1\"\n",
		Source: v1alpha1.AddonSource{Helm: &v1alpha1.HelmSource{Repo: srv.URL, Chart: "hooked", Version: "1.0.0"}},
	}}); err != nil {
		t.Fatal(err)
	}
	if appValue() != "" {
		t.Fatal("objects applied before the pre-install hook finished")
	}
	finishPod(corev1.PodSucceeded)
	eventually(t, "installed", func() bool { p, _ := addonPhase(t, c, "hooked"); return p == v1alpha1.AddonSynced && appValue() == "1" })
	_, a := addonPhase(t, c, "hooked")
	if len(a.Status.HookRuns) != 1 || a.Status.HookRuns[0].Event != "pre-install" || a.Status.AppliedHash == "" {
		t.Fatalf("hook runs %+v hash %q", a.Status.HookRuns, a.Status.AppliedHash)
	}
	// A resync with nothing new runs no hooks.
	updateAddon(t, c, "hooked", func(a *v1alpha1.Addon) { a.Annotations = map[string]string{"poke": "1"} })
	eventually(t, "resynced", func() bool { _, a := addonPhase(t, c, "hooked"); return a.Status.ObservedGeneration == a.Generation })
	if _, a := addonPhase(t, c, "hooked"); len(a.Status.HookRuns) != 1 {
		t.Fatalf("a resync ran hooks: %+v", a.Status.HookRuns)
	}

	// Upgrade (new values): the old hook pod is replaced, and the
	// post-upgrade hook runs after the objects and is then deleted.
	updateAddon(t, c, "hooked", func(a *v1alpha1.Addon) { a.Spec.Values = "v: \"2\"\n" })
	finishPod(corev1.PodSucceeded)
	eventually(t, "upgraded", func() bool {
		_, a := addonPhase(t, c, "hooked")
		return appValue() == "2" && len(a.Status.HookRuns) == 3
	})
	_, a = addonPhase(t, c, "hooked")
	var events []string
	for _, r := range a.Status.HookRuns {
		events = append(events, r.Event+":"+r.Name+":"+r.Status)
	}
	if strings.Join(events, " ") != "pre-install:migrate:succeeded pre-upgrade:migrate:succeeded post-upgrade:post-upgrade-marker:succeeded" {
		t.Errorf("hook runs = %v", events)
	}
	if err := c.Get(ctx, client.ObjectKey{Namespace: "hooked", Name: "post-upgrade-marker"}, &corev1.ConfigMap{}); !apierrors.IsNotFound(err) {
		t.Error("hook-succeeded: the post-upgrade hook object is deleted once it succeeded")
	}

	// A failing pre-upgrade hook stops the upgrade: nothing is applied.
	updateAddon(t, c, "hooked", func(a *v1alpha1.Addon) { a.Spec.Values = "v: \"3\"\n" })
	finishPod(corev1.PodFailed)
	eventually(t, "upgrade stopped", func() bool {
		p, a := addonPhase(t, c, "hooked")
		return p == v1alpha1.AddonError && strings.Contains(a.Status.Message, "pre-upgrade hook migrate failed")
	})
	if appValue() != "2" {
		t.Fatal("a failed pre-upgrade hook must leave the objects as they were")
	}
}
