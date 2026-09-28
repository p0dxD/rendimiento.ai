package addon

import (
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/p0dxD/rendimiento.ai/api/v1alpha1"
)

// chartRepo serves a one-chart Helm repository with a relative chart URL.
func chartRepo(t *testing.T) *httptest.Server {
	t.Helper()
	c := &chart.Chart{
		Metadata: &chart.Metadata{APIVersion: "v2", Name: "demo", Version: "1.2.3"},
		Values:   map[string]any{"replicas": 1, "greeting": "hi"},
		Templates: []*chart.File{
			{Name: "templates/deploy.yaml", Data: []byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ .Release.Name }}
  labels: { app.kubernetes.io/managed-by: {{ .Release.Service }} }
spec:
  replicas: {{ .Values.replicas }}
  selector: { matchLabels: { app: demo } }
  template:
    metadata: { labels: { app: demo } }
    spec: { containers: [{ name: c, image: "demo:{{ .Values.greeting }}" }] }
`)},
			{Name: "templates/hook.yaml", Data: []byte(`apiVersion: batch/v1
kind: Job
metadata:
  name: {{ .Release.Name }}-migrate
  annotations: { "helm.sh/hook": pre-install }
spec: { template: { spec: { restartPolicy: Never, containers: [{ name: m, image: busybox }] } } }
`)},
		},
		Files: []*chart.File{{Name: "crds/widget.yaml", Data: []byte(`apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: { name: widgets.example.com }
spec:
  group: example.com
  names: { kind: Widget, plural: widgets }
  scope: Namespaced
  versions: [{ name: v1, served: true, storage: true, schema: { openAPIV3Schema: { type: object } } }]
`)}},
	}
	dir := t.TempDir()
	archive, err := chartutil.Save(c, dir)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(archive)
	index := `apiVersion: v1
entries:
  demo:
    - apiVersion: v2
      name: demo
      version: 1.2.3
      urls: [charts/demo-1.2.3.tgz]
`
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repo/index.yaml":
			w.Write([]byte(index))
		case "/repo/charts/demo-1.2.3.tgz":
			w.Write(data)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestRenderHelm(t *testing.T) {
	srv := chartRepo(t)
	defer srv.Close()
	r := &Renderer{}
	a := &v1alpha1.Addon{ObjectMeta: metav1.ObjectMeta{Name: "demo"}, Spec: v1alpha1.AddonSpec{
		Namespace: "tools", ReleaseName: "legacy-name", Values: "replicas: 3\n",
		Source: v1alpha1.AddonSource{Helm: &v1alpha1.HelmSource{Repo: srv.URL + "/repo/", Chart: "demo", Version: "v1.2.3"}},
	}}
	res, err := r.Render(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	kinds := []string{}
	for _, o := range res.Objects {
		kinds = append(kinds, o.GetKind()+"/"+o.GetName())
	}
	if got := strings.Join(kinds, " "); got != "CustomResourceDefinition/widgets.example.com Deployment/legacy-name" {
		t.Fatalf("objects = %s", got)
	}
	dep := res.Objects[1]
	if n, _, _ := unstructuredInt(dep.Object, "spec", "replicas"); n != 3 {
		t.Errorf("values not applied: replicas = %d", n)
	}
	if dep.GetLabels()["app.kubernetes.io/managed-by"] != "Helm" {
		t.Error("rendered like helm template: .Release.Service is Helm")
	}
	if res.Revision != "demo-1.2.3" || len(res.SkippedHooks) != 1 || !strings.Contains(res.SkippedHooks[0], "pre-install") {
		t.Errorf("revision %s hooks %v", res.Revision, res.SkippedHooks)
	}
	a.Spec.Source.Helm.Version = "9.9.9"
	if _, err := r.Render(context.Background(), a); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("missing version: %v", err)
	}
}

func unstructuredInt(obj map[string]any, fields ...string) (int64, bool, error) {
	cur := any(obj)
	for _, f := range fields {
		m, ok := cur.(map[string]any)
		if !ok {
			return 0, false, nil
		}
		cur = m[f]
	}
	switch v := cur.(type) {
	case int64:
		return v, true, nil
	case float64:
		return int64(v), true, nil
	}
	return 0, false, nil
}

type fakeGit map[string]fs.FS

func (f fakeGit) Fetch(_ context.Context, repo, path, ref string) (fs.FS, string, error) {
	return f[repo+"/"+path], "abc123", nil
}

func TestRenderGitKustomize(t *testing.T) {
	dir := fstest.MapFS{
		"kustomization.yaml": {Data: []byte("resources: [deploy.yaml, svc.yaml]\nnamespace: umami\n")},
		"deploy.yaml":        {Data: []byte("apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: umami}\nspec: {selector: {matchLabels: {a: b}}, template: {metadata: {labels: {a: b}}, spec: {containers: [{name: c, image: x}]}}}\n")},
		"svc.yaml":           {Data: []byte("apiVersion: v1\nkind: Service\nmetadata: {name: umami}\nspec: {ports: [{port: 80}]}\n")},
		"unused.yaml":        {Data: []byte("apiVersion: v1\nkind: ConfigMap\nmetadata: {name: not-listed}\n")},
	}
	r := &Renderer{Git: fakeGit{"p0dxD/gitops/umami": dir}}
	res, err := r.Render(context.Background(), &v1alpha1.Addon{ObjectMeta: metav1.ObjectMeta{Name: "umami"}, Spec: v1alpha1.AddonSpec{
		Namespace: "umami", Source: v1alpha1.AddonSource{Git: &v1alpha1.GitSource{Repo: "p0dxD/gitops", Path: "umami"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Objects) != 2 || res.Revision != "abc123" || res.Objects[0].GetNamespace() != "umami" {
		t.Fatalf("objects %d revision %s ns %s", len(res.Objects), res.Revision, res.Objects[0].GetNamespace())
	}
}

func TestBuildPlainManifests(t *testing.T) {
	dir := fstest.MapFS{
		"a.yaml":    {Data: []byte("apiVersion: v1\nkind: ConfigMap\nmetadata: {name: a}\n---\n---\napiVersion: v1\nkind: List\nitems:\n- {apiVersion: v1, kind: ConfigMap, metadata: {name: b}}\n")},
		"notes.txt": {Data: []byte("ignored")},
	}
	objs, err := Build(dir)
	if err != nil || len(objs) != 2 || objs[1].GetName() != "b" {
		t.Fatalf("objs = %v, %v", objs, err)
	}
}
