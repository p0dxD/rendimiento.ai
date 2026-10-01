package platform

import (
	"context"

	"github.com/go-logr/logr"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/p0dxD/rendimiento.ai/api/v1alpha1"
	"github.com/p0dxD/rendimiento.ai/internal/controller"
	"github.com/p0dxD/rendimiento.ai/internal/dns"
	"github.com/p0dxD/rendimiento.ai/internal/events"
	"github.com/p0dxD/rendimiento.ai/internal/generate"
	gh "github.com/p0dxD/rendimiento.ai/internal/github"
	"github.com/p0dxD/rendimiento.ai/internal/pipeline"
	"github.com/p0dxD/rendimiento.ai/internal/render"
	"github.com/p0dxD/rendimiento.ai/internal/spec"
	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// fakeGitHub holds one repo whose files change per ref.
type fakeGitHub struct {
	mu      sync.Mutex
	files   map[string]fstest.MapFS // ref → tree
	prs     []map[string]string
	checks  map[int64]gh.CheckRun
	changed map[string][]string // "base...head" → changed files
}

func (f *fakeGitHub) tree(ref string) fstest.MapFS {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.files[ref]; ok {
		return t
	}
	return f.files["main"]
}
func (f *fakeGitHub) FileAt(_ context.Context, _ int64, _, path, ref string) ([]byte, error) {
	b, err := fs.ReadFile(f.tree(ref), path)
	if err != nil {
		return nil, &gh.APIError{Status: 404}
	}
	return b, nil
}
func (f *fakeGitHub) RepoFS(_ context.Context, _ int64, _, ref string) (fs.FS, error) {
	return f.tree(ref), nil
}
func (f *fakeGitHub) OpenPR(_ context.Context, _ int64, _, _, _, _, _ string, files map[string]string) (*gh.PullRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prs = append(f.prs, files)
	// "Merge" it: the files land on main.
	for p, c := range files {
		f.files["main"][p] = &fstest.MapFile{Data: []byte(c)}
	}
	return &gh.PullRequest{Number: 1, HTMLURL: "https://github.com/p0dxD/hello/pull/1"}, nil
}
func (f *fakeGitHub) CreateCheck(_ context.Context, _ int64, _ string, cr gh.CheckRun) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := int64(len(f.checks) + 1)
	f.checks[id] = cr
	return id, nil
}
func (f *fakeGitHub) UpdateCheck(_ context.Context, _ int64, _ string, id int64, cr gh.CheckRun) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checks[id] = cr
	return nil
}
func (f *fakeGitHub) CloneToken(context.Context, int64) (string, error) { return "ghs_x", nil }
func (f *fakeGitHub) BranchSHA(context.Context, int64, string, string) (string, error) {
	return strings.Repeat("9", 40), nil
}
func (f *fakeGitHub) ChangedFiles(_ context.Context, _ int64, _, base, head string) ([]string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	files, ok := f.changed[base+"..."+head]
	if !ok {
		return nil, false, nil // unknown diff: treat as incomplete
	}
	return files, true, nil
}

// fakeExec "builds" by returning a digest derived from the commit.
type fakeExec struct {
	failSHA   string
	failSteps map[string]bool // step IDs that fail
	tokens    []string
	ran       []string // step IDs executed
	p         *Platform
	mu        sync.Mutex
}

func (e *fakeExec) Execute(ctx context.Context, _ string, src pipeline.Source, s pipeline.Step, w io.Writer) pipeline.StepResult {
	tok, _ := e.p.CloneToken(ctx, src.Repo)
	e.mu.Lock()
	e.tokens = append(e.tokens, tok)
	e.ran = append(e.ran, s.ID)
	e.mu.Unlock()
	io.WriteString(w, "running "+s.ID+"\n")
	if src.SHA == e.failSHA && s.Kind == pipeline.KindTest {
		return pipeline.StepResult{Status: pipeline.StatusFailed, Message: "tests failed"}
	}
	if e.failSteps[s.ID] {
		return pipeline.StepResult{Status: pipeline.StatusFailed, Message: "task failed"}
	}
	return pipeline.StepResult{Status: pipeline.StatusSucceeded, Digest: "sha256:" + src.SHA[:8]}
}

func setup(t *testing.T) (*Platform, *fakeGitHub, *fakeExec, client.Client) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctrl.SetLogger(logr.Discard())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := resetDB(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	env := &envtest.Environment{CRDDirectoryPaths: []string{filepath.Join("..", "..", "deploy", "crds")}, ErrorIfCRDPathMissing: true}
	cfg, err := env.Start()
	if err != nil {
		if os.Getenv("KUBEBUILDER_ASSETS") != "" {
			// Configured but failed to start (e.g. an overloaded machine):
			// fail, so a test gate never passes with these tests skipped.
			t.Fatalf("envtest failed to start: %v", err)
		}
		t.Skipf("envtest unavailable: %v", err)
	}
	t.Cleanup(func() { _ = env.Stop() })
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)
	skip := true
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{Scheme: scheme, Metrics: metricsserver.Options{BindAddress: "0"}, Controller: config.Controller{SkipNameValidation: &skip}})
	if err != nil {
		t.Fatal(err)
	}
	if err := (&controller.AppReconciler{Client: mgr.GetClient(), Scheme: scheme, Render: render.DefaultOptions(), DNS: dns.Noop{}}).SetupWithManager(mgr); err != nil {
		t.Fatal(err)
	}
	go func() { _ = mgr.Start(ctx) }()
	kube, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}

	gh := &fakeGitHub{checks: map[int64]gh.CheckRun{}, files: map[string]fstest.MapFS{
		"main": {"go.mod": {Data: []byte("module hello\ngo 1.23\n")}, "main.go": {Data: []byte("package main")}},
	}}
	p := &Platform{
		Store: st, Kube: kube, GitHub: gh, Generator: generate.Templates{}, Hub: events.NewHub(),
		Config: Config{Registry: "registry.cube.local:5000", BaseURL: "https://rendimiento.joserod.space", Zone: "joserod.space"},
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	exec := &fakeExec{p: p}
	p.Runner = pipeline.NewRunner(exec, p, 2)
	go p.Work(ctx)
	return p, gh, exec, kube
}

func resetDB(ctx context.Context, dsn string) error {
	st, err := store.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer st.Close()
	return st.ResetForTests(ctx)
}

func waitRun(t *testing.T, p *Platform, id int64) *store.Run {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		r, err := p.Store.GetRun(context.Background(), id)
		if err == nil && r.FinishedAt != nil {
			return r
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("run %d did not finish", id)
	return nil
}

func TestOnboardPushDeployRollback(t *testing.T) {
	p, fake, exec, kube := setup(t)
	ctx := context.Background()
	sha1 := strings.Repeat("a", 40)
	sha2 := strings.Repeat("b", 40)
	sha3 := strings.Repeat("c", 40)

	// 1. Wizard: detect and propose.
	prop, err := p.Propose(ctx, 7, "p0dxD/hello", "main")
	if err != nil {
		t.Fatal(err)
	}
	if prop.Existing || prop.Spec.Services[0].Domain != "hello.joserod.space" || prop.Files["Dockerfile"] == "" {
		t.Fatalf("proposal = %+v", prop)
	}

	// 2. Confirm: app registered, App object waiting, PR opened.
	res, err := p.Onboard(ctx, OnboardRequest{Installation: 7, Repo: "p0dxD/hello", DefaultBranch: "main", Name: "hello", Spec: prop.Spec, Files: prop.Files}, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.PR == nil || !strings.Contains(fake.prs[0]["rendimiento.yaml"], "hello.joserod.space") {
		t.Fatalf("PR files = %v", fake.prs)
	}
	if _, err := p.Onboard(ctx, OnboardRequest{Installation: 7, Repo: "x/y", DefaultBranch: "main", Name: "hello", Spec: prop.Spec}, false); err == nil {
		t.Fatal("duplicate app name accepted")
	}

	// 3. PR merged → push to main → build → release #1 → App object points at it.
	fake.files[sha1] = fake.files["main"]
	runs, err := p.HandlePush(ctx, PushEvent{Installation: 7, Repo: "P0DXD/hello", Branch: "main", SHA: sha1})
	if err != nil || len(runs) != 1 || !runs[0].Deploy {
		t.Fatalf("runs = %+v %v", runs, err)
	}
	if r := waitRun(t, p, runs[0].ID); r.Status != store.RunSucceeded || r.Message != "released #1" {
		t.Fatalf("run = %+v", r)
	}
	var cr v1alpha1.App
	if err := kube.Get(ctx, client.ObjectKey{Name: "hello"}, &cr); err != nil {
		t.Fatal(err)
	}
	if cr.Spec.Release != 1 || cr.Spec.Images["hello"] != "registry.cube.local:5000/hello-hello@sha256:aaaaaaaa" {
		t.Fatalf("App spec = %+v", cr.Spec)
	}
	eventually(t, "deployment from release 1", func() bool {
		var d appsv1.Deployment
		return kube.Get(ctx, client.ObjectKey{Namespace: "hello", Name: "hello"}, &d) == nil &&
			strings.HasSuffix(d.Spec.Template.Spec.Containers[0].Image, "@sha256:aaaaaaaa")
	})
	if log, _, _ := p.Store.StepLog(ctx, runs[0].ID, "hello:build"); log != "running hello:build\n" {
		t.Fatalf("log = %q", log)
	}
	if c := fake.checks[1]; c.Conclusion != "success" {
		t.Fatalf("check = %+v", c)
	}
	if exec.tokens[0] != "ghs_x" {
		t.Fatal("clone token not passed to executor")
	}

	// 4. Feature branch push: builds, never deploys.
	fake.files[sha2] = fake.files["main"]
	runs, _ = p.HandlePush(ctx, PushEvent{Installation: 7, Repo: "p0dxD/hello", Branch: "feature", SHA: sha2})
	if r := waitRun(t, p, runs[0].ID); r.Deploy || r.Status != store.RunSucceeded {
		t.Fatalf("branch run = %+v", r)
	}
	_ = kube.Get(ctx, client.ObjectKey{Name: "hello"}, &cr)
	if cr.Spec.Release != 1 {
		t.Fatal("branch push deployed")
	}

	// 5. Release #2, then a failing commit that must not deploy.
	fake.files[sha2] = fake.files["main"]
	runs, _ = p.HandlePush(ctx, PushEvent{Installation: 7, Repo: "p0dxD/hello", Branch: "main", SHA: sha2})
	waitRun(t, p, runs[0].ID)
	exec.failSHA = sha3
	// Add a test step so the failure is in "test".
	fake.files[sha3] = fstest.MapFS{"rendimiento.yaml": {Data: []byte("services:\n  - name: hello\n    domain: hello.joserod.space\n    test: {image: golang:1.23, command: go test ./...}\n")}}
	runs, _ = p.HandlePush(ctx, PushEvent{Installation: 7, Repo: "p0dxD/hello", Branch: "main", SHA: sha3})
	r := waitRun(t, p, runs[0].ID)
	if r.Status != store.RunFailed {
		t.Fatalf("failing run = %+v", r)
	}
	for _, s := range r.Steps {
		if s.ID == "hello:build" && s.Status != "skipped" {
			t.Fatalf("build after failed test = %+v", s)
		}
	}
	_ = kube.Get(ctx, client.ObjectKey{Name: "hello"}, &cr)
	if cr.Spec.Release != 2 {
		t.Fatalf("release = %d, failed run must not deploy", cr.Spec.Release)
	}

	// 6. One-click rollback to #1 creates release #3 with #1's images.
	rel, err := p.Rollback(ctx, res.App, 1)
	if err != nil || rel.Number != 3 || *rel.RollbackOf != 1 {
		t.Fatalf("rollback = %+v %v", rel, err)
	}
	eventually(t, "rolled back deployment", func() bool {
		var d appsv1.Deployment
		return kube.Get(ctx, client.ObjectKey{Namespace: "hello", Name: "hello"}, &d) == nil &&
			strings.HasSuffix(d.Spec.Template.Spec.Containers[0].Image, "@sha256:aaaaaaaa")
	})

	// 7. Pushes to repos that are not onboarded are ignored.
	if runs, err := p.HandlePush(ctx, PushEvent{Repo: "p0dxD/other", Branch: "main", SHA: sha1}); err != nil || len(runs) != 0 {
		t.Fatalf("unknown repo: %v %v", runs, err)
	}
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

func TestProposeDetectsExistingDeployment(t *testing.T) {
	p, _, _, kube := setup(t)
	ctx := context.Background()
	one := int32(1)
	lbl := map[string]string{"app": "secplus"}
	for _, o := range []client.Object{
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "secplus"}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "secplus", Name: "secplus",
			Annotations: map[string]string{"argocd.argoproj.io/tracking-id": "secplus:apps/Deployment:secplus/secplus"}},
			Spec: appsv1.DeploymentSpec{Replicas: &one, Selector: &metav1.LabelSelector{MatchLabels: lbl},
				Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: lbl}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "registry.cube.local:5000/secplus:1.0.8"}}}}}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "secplus", Name: "secplus-practice"}, Type: corev1.SecretTypeOpaque},
	} {
		if err := kube.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	prop, err := p.Propose(ctx, 7, "p0dxD/secplus", "main")
	if err != nil {
		t.Fatal(err)
	}
	m := prop.Migration
	if m == nil || m.ArgoCD != "secplus" || m.Managed || len(m.Deployments) != 1 || !strings.Contains(m.Deployments[0], "secplus:1.0.8") || m.Secrets[0] != "secplus-practice" {
		t.Fatalf("migration = %+v", m)
	}
	// A new repo has no namespace yet.
	if prop, _ := p.Propose(ctx, 7, "p0dxD/brand-new", "main"); prop.Migration != nil {
		t.Fatalf("unexpected migration %+v", prop.Migration)
	}

	// Onboarding with adopt passes consent to the App object.
	if _, err := p.Onboard(ctx, OnboardRequest{Installation: 7, Repo: "p0dxD/secplus", DefaultBranch: "main", Name: "secplus",
		Spec: prop.Spec, Files: prop.Files, Adopt: true}, false); err != nil {
		t.Fatal(err)
	}
	var cr v1alpha1.App
	if err := kube.Get(ctx, client.ObjectKey{Name: "secplus"}, &cr); err != nil || !cr.Spec.Adopt {
		t.Fatalf("App adopt = %v, %v", cr.Spec.Adopt, err)
	}
}

// Only what changed since the last release is tested, built and rolled.
func TestChangeDetection(t *testing.T) {
	p, fake, exec, kube := setup(t)
	ctx := context.Background()
	yaml := `services:
  - name: ui
    path: ui
    test: {image: node:20, command: npm test}
  - name: api
    path: api
    watch: [shared]
  - name: db
    image: postgres:15-alpine
jobs:
  - name: scraper
    schedule: "@daily"
    path: congress
`
	sha := func(c byte) string { return strings.Repeat(string(c), 40) }
	tree := fstest.MapFS{"rendimiento.yaml": {Data: []byte(yaml)}}
	for _, c := range "abcdef9" {
		fake.files[sha(byte(c))] = tree
	}
	fake.files["main"] = tree
	fake.changed = map[string][]string{
		sha('a') + "..." + sha('b'): {"ui/src/App.tsx"},
		sha('b') + "..." + sha('c'): {"shared/util.py", "README.md"},
		sha('c') + "..." + sha('d'): {"rendimiento.yaml"},
	}
	sp, _ := spec.Parse([]byte(yaml))
	if _, err := p.Onboard(ctx, OnboardRequest{Installation: 7, Repo: "p0dxD/shop", DefaultBranch: "main", Name: "shop", Spec: *sp}, false); err != nil {
		t.Fatal(err)
	}
	push := func(c byte) (*store.Run, []string) {
		t.Helper()
		exec.mu.Lock()
		exec.ran = nil
		exec.mu.Unlock()
		runs, err := p.HandlePush(ctx, PushEvent{Installation: 7, Repo: "p0dxD/shop", Branch: "main", SHA: sha(c)})
		if err != nil || len(runs) != 1 {
			t.Fatalf("push: %v %v", runs, err)
		}
		r := waitRun(t, p, runs[0].ID)
		exec.mu.Lock()
		defer exec.mu.Unlock()
		ran := append([]string(nil), exec.ran...)
		sort.Strings(ran)
		return r, ran
	}
	images := func() map[string]string {
		var cr v1alpha1.App
		_ = kube.Get(ctx, client.ObjectKey{Name: "shop"}, &cr)
		return cr.Spec.Images
	}

	// 1. First release: everything builds.
	r, ran := push('a')
	if r.Status != store.RunSucceeded || strings.Join(ran, ",") != "api:build,job-scraper:build,ui:build,ui:test" {
		t.Fatalf("first release ran %v (%s %s)", ran, r.Status, r.Message)
	}
	first := images()

	// 2. UI-only change: only the UI is tested and built; the rest is reused.
	r, ran = push('b')
	if r.Status != store.RunSucceeded || strings.Join(ran, ",") != "ui:build,ui:test" {
		t.Fatalf("ui-only push ran %v (%s %s)", ran, r.Status, r.Message)
	}
	second := images()
	if second["api"] != first["api"] || second["job:scraper"] != first["job:scraper"] || second["ui"] == first["ui"] || second["db"] != "postgres:15-alpine" {
		t.Fatalf("images after ui-only push: %v (before %v)", second, first)
	}
	for _, st := range r.Steps {
		if st.Service == "api" && (st.Status != "reused" || !strings.Contains(st.Message, "release #1")) {
			t.Fatalf("api step = %+v", st)
		}
	}

	// 3. A change in a watched path rebuilds that service.
	if _, ran = push('c'); strings.Join(ran, ",") != "api:build" {
		t.Fatalf("shared/ change ran %v", ran)
	}
	// 4. rendimiento.yaml changed: everything builds.
	if _, ran = push('d'); len(ran) != 4 {
		t.Fatalf("rendimiento.yaml change ran %v", ran)
	}
	// 5. Unknown or truncated diff: everything builds.
	if _, ran = push('e'); len(ran) != 4 {
		t.Fatalf("unknown diff ran %v", ran)
	}
	// 6. Manual "Build now": everything builds.
	app, _ := p.Store.GetApp(ctx, "shop")
	run, err := p.QueueRun(ctx, app, "main", "", "manual")
	if err != nil {
		t.Fatal(err)
	}
	exec.mu.Lock()
	exec.ran = nil
	exec.mu.Unlock()
	waitRun(t, p, run.ID)
	exec.mu.Lock()
	n := len(exec.ran)
	exec.mu.Unlock()
	if n != 4 {
		t.Fatalf("manual build ran %d steps", n)
	}
}

func TestTasks(t *testing.T) {
	p, fake, exec, _ := setup(t)
	ctx := context.Background()
	yaml := `services:
  - name: api
    path: api
tasks:
  - name: mobile
    image: node:20
    command: npx eas-cli build
    path: mobile
  - name: smoke
    image: curl
    command: curl -f x
    after: [api]
    when: always
    optional: true
`
	sha := func(c byte) string { return strings.Repeat(string(c), 40) }
	tree := fstest.MapFS{"rendimiento.yaml": {Data: []byte(yaml)}}
	for _, c := range "abcf" {
		fake.files[sha(byte(c))] = tree
	}
	fake.files["main"] = tree
	fake.changed = map[string][]string{sha('a') + "..." + sha('b'): {"api/main.go"}}
	sp, _ := spec.Parse([]byte(yaml))
	if _, err := p.Onboard(ctx, OnboardRequest{Installation: 7, Repo: "p0dxD/shop", DefaultBranch: "main", Name: "shop", Spec: *sp}, false); err != nil {
		t.Fatal(err)
	}
	push := func(branch string, c byte) (*store.Run, []string) {
		t.Helper()
		exec.mu.Lock()
		exec.ran = nil
		exec.mu.Unlock()
		runs, err := p.HandlePush(ctx, PushEvent{Installation: 7, Repo: "p0dxD/shop", Branch: branch, SHA: sha(c)})
		if err != nil || len(runs) != 1 {
			t.Fatalf("push: %v %v", runs, err)
		}
		r := waitRun(t, p, runs[0].ID)
		exec.mu.Lock()
		defer exec.mu.Unlock()
		ran := append([]string(nil), exec.ran...)
		sort.Strings(ran)
		return r, ran
	}
	step := func(r *store.Run, id string) store.Step {
		for _, st := range r.Steps {
			if st.ID == id {
				return st
			}
		}
		t.Fatalf("no step %s in %+v", id, r.Steps)
		return store.Step{}
	}

	// 1. First release: everything runs; the optional task fails without failing the run.
	exec.failSteps = map[string]bool{"smoke:task": true}
	r, ran := push("main", 'a')
	if r.Status != store.RunSucceeded || strings.Join(ran, ",") != "api:build,mobile:task,smoke:task" {
		t.Fatalf("first push ran %v (%s %s)", ran, r.Status, r.Message)
	}
	// 2. A change to api only: mobile is skipped as unchanged; smoke runs after the new build.
	exec.failSteps = nil
	r, ran = push("main", 'b')
	if strings.Join(ran, ",") != "api:build,smoke:task" || !strings.Contains(step(r, "mobile:task").Message, "nothing under mobile changed") {
		t.Fatalf("api-only push ran %v; mobile = %+v", ran, step(r, "mobile:task"))
	}
	// 3. A branch: the deploy-only task never runs there (it may read secrets).
	r, ran = push("feature", 'f')
	if strings.Join(ran, ",") != "api:build,smoke:task" || !strings.Contains(step(r, "mobile:task").Message, "default branch") {
		t.Fatalf("branch push ran %v; mobile = %+v", ran, step(r, "mobile:task"))
	}
	// 4. A required task that fails fails the run: no release.
	exec.failSteps = map[string]bool{"mobile:task": true}
	fake.changed[sha('b')+"..."+sha('c')] = []string{"mobile/app.json"}
	if r, _ = push("main", 'c'); r.Status != store.RunFailed || !strings.Contains(r.Message, "mobile:task") {
		t.Fatalf("failed task: %s %s", r.Status, r.Message)
	}
}

// Disconnect leaves everything running and unowned; the wizard suggests the
// namespace that already serves a repo's hosts.
func TestDisconnectAndSuggestedName(t *testing.T) {
	p, fake, _, kube := setup(t)
	ctx := context.Background()
	sha := strings.Repeat("a", 40)
	yaml := "services:\n  - name: web\n    domain: shop.joserod.space\n"
	fake.files["main"] = fstest.MapFS{"rendimiento.yaml": {Data: []byte(yaml)}}
	fake.files[sha] = fake.files["main"]
	sp, _ := spec.Parse([]byte(yaml))
	if _, err := p.Onboard(ctx, OnboardRequest{Installation: 7, Repo: "p0dxD/shop", DefaultBranch: "main", Name: "shop", Spec: *sp}, false); err != nil {
		t.Fatal(err)
	}
	runs, _ := p.HandlePush(ctx, PushEvent{Installation: 7, Repo: "p0dxD/shop", Branch: "main", SHA: sha})
	waitRun(t, p, runs[0].ID)
	key := client.ObjectKey{Namespace: "shop", Name: "web"}
	eventually(t, "deployed", func() bool { return kube.Get(ctx, key, &appsv1.Deployment{}) == nil })

	im, err := p.DeleteImpact(ctx, mustApp(t, p, "shop"))
	if err != nil || im.Namespace != "shop" {
		t.Fatalf("impact = %+v %v", im, err)
	}
	if err := p.DisconnectApp(ctx, mustApp(t, p, "shop")); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Store.GetApp(ctx, "shop"); err == nil {
		t.Fatal("app record should be gone")
	}
	eventually(t, "App deleted (after the controller's finalizer)", func() bool {
		return apierrors.IsNotFound(kube.Get(ctx, client.ObjectKey{Name: "shop"}, &v1alpha1.App{}))
	})
	var d appsv1.Deployment
	var ns corev1.Namespace
	if err := kube.Get(ctx, key, &d); err != nil {
		t.Fatalf("deployment must keep running: %v", err)
	}
	_ = kube.Get(ctx, client.ObjectKey{Name: "shop"}, &ns)
	for _, o := range []client.Object{&d, &ns} {
		if len(o.GetOwnerReferences()) != 0 || o.GetLabels()["app.kubernetes.io/managed-by"] == "rendimiento" {
			t.Fatalf("%T still owned/labeled: %v %v", o, o.GetOwnerReferences(), o.GetLabels())
		}
	}
	// It now looks like any unmanaged app: the wizard offers to migrate it
	// back, found by its host even though the repo is named differently.
	prop, err := p.Propose(ctx, 7, "p0dxD/storefront-repo", "main")
	if err != nil {
		t.Fatal(err)
	}
	if prop.SuggestedName != "shop" || prop.Migration == nil || prop.Migration.Namespace != "shop" {
		t.Fatalf("suggested %q migration %+v", prop.SuggestedName, prop.Migration)
	}
}

func mustApp(t *testing.T, p *Platform, name string) *store.App {
	t.Helper()
	a, err := p.Store.GetApp(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
