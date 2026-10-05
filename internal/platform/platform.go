// Package platform is the orchestration core: it turns GitHub events into
// CI runs, successful default-branch runs into releases, and releases into
// App objects that the controller deploys.
package platform

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/p0dxD/rendimiento.ai/api/v1alpha1"
	"github.com/p0dxD/rendimiento.ai/internal/controller"
	"github.com/p0dxD/rendimiento.ai/internal/detect"
	"github.com/p0dxD/rendimiento.ai/internal/events"
	"github.com/p0dxD/rendimiento.ai/internal/generate"
	gh "github.com/p0dxD/rendimiento.ai/internal/github"
	"k8s.io/client-go/kubernetes"

	"github.com/p0dxD/rendimiento.ai/internal/notify"
	"github.com/p0dxD/rendimiento.ai/internal/pipeline"
	"github.com/p0dxD/rendimiento.ai/internal/render"
	"github.com/p0dxD/rendimiento.ai/internal/spec"
	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// GitHub is the subset of the GitHub App the platform needs; faked in tests.
type GitHub interface {
	FileAt(ctx context.Context, installation int64, repo, path, ref string) ([]byte, error)
	RepoFS(ctx context.Context, installation int64, repo, ref string) (fs.FS, error)
	OpenPR(ctx context.Context, installation int64, repo, base, branch, title, body string, files map[string]string) (*gh.PullRequest, error)
	CreateCheck(ctx context.Context, installation int64, repo string, cr gh.CheckRun) (int64, error)
	UpdateCheck(ctx context.Context, installation int64, repo string, id int64, cr gh.CheckRun) error
	CloneToken(ctx context.Context, installation int64) (string, error)
	ChangedFiles(ctx context.Context, installation int64, repo, base, head string) ([]string, bool, error)
	BranchSHA(ctx context.Context, installation int64, repo, branch string) (string, error)
}

// Config is the platform's cluster-level settings.
type Config struct {
	Registry string // registry.example.lan:5000
	BaseURL  string // public URL of the UI, for check-run links
	Zone     string // default DNS zone for generated domains
	// Railpack means build pods can build services that have no Dockerfile.
	Railpack bool
}

// Platform connects GitHub, the store, CI and the cluster: webhooks become runs, runs become
// releases, releases become App objects.
type Platform struct {
	Store     *store.Store
	Kube      client.Client
	GitHub    GitHub
	Runner    *pipeline.Runner
	Generator generate.Generator
	Hub       *events.Hub
	Config    Config
	Log       *slog.Logger
	// Verify configures release verification and automatic rollback.
	Verify VerifySettings
	// Notify emails failed runs and rolled-back releases (nil: no email).
	Notify *notify.Notifier
	// Clientset runs post-deploy tasks (Jobs in app namespaces) and reads their logs.
	Clientset kubernetes.Interface

	mu        sync.Mutex
	cancels   map[int64]context.CancelFunc      // running run → cancel
	verifying map[int64]context.CancelCauseFunc // app → its running verification
}

// ---- onboarding ----

// Proposal is what the wizard shows before anything is written anywhere.
type Proposal struct {
	Repo          string            `json:"repo"`
	DefaultBranch string            `json:"defaultBranch"`
	Detected      []detect.Result   `json:"detected"`
	Spec          spec.Spec         `json:"spec"`
	Files         map[string]string `json:"files"`
	// Existing is true when the repo already has rendimiento.yaml (its content is used).
	Existing bool `json:"existing"`
	// Migration is set when the app's namespace already exists: the app is
	// running some other way (ArgoCD, kubectl) and can be adopted.
	Migration *Migration `json:"migration,omitempty"`
	// SuggestedName is the namespace already serving the spec's hosts when it
	// differs from the repo name (e.g. repo wellbeingportal.app → "wellness").
	SuggestedName string `json:"suggestedName,omitempty"`
	// Railpack is true when services without a Dockerfile can be built
	// straight from source; the generated Dockerfiles are then optional.
	Railpack bool `json:"railpack"`
}

// Migration describes what is running in a namespace rendimiento would adopt.
type Migration struct {
	Namespace   string   `json:"namespace"`
	Managed     bool     `json:"managed"`               // already a rendimiento app
	ArgoCD      string   `json:"argocd,omitempty"`      // Argo Application tracking it, if any
	Deployments []string `json:"deployments,omitempty"` // name (image)
	Hosts       []string `json:"hosts,omitempty"`
	Secrets     []string `json:"secrets,omitempty"`
}

// namespaceServing returns the namespace of an ingress not managed by
// rendimiento that serves one of the spec's hosts, or "".
func (p *Platform) namespaceServing(ctx context.Context, sp spec.Spec) (string, error) {
	hosts := map[string]bool{}
	for _, svc := range sp.Services {
		for _, h := range svc.Hosts() {
			hosts[h] = true
		}
	}
	if len(hosts) == 0 {
		return "", nil
	}
	var ings networkingv1.IngressList
	if err := p.Kube.List(ctx, &ings); err != nil {
		return "", err
	}
	for _, ing := range ings.Items {
		if ing.Labels[render.LabelManagedBy] == render.ManagedBy {
			continue
		}
		for _, r := range ing.Spec.Rules {
			if hosts[r.Host] {
				return ing.Namespace, nil
			}
		}
	}
	return "", nil
}

// argoApp returns the ArgoCD Application tracking an object: the
// tracking-id annotation (Argo CD 2.x annotation mode and 3.x default) or
// the legacy instance label.
func argoApp(m metav1.ObjectMeta) string {
	if id := m.Annotations["argocd.argoproj.io/tracking-id"]; id != "" {
		app, _, _ := strings.Cut(id, ":")
		return app
	}
	return m.Labels["app.kubernetes.io/instance"]
}

// InspectNamespace reports what already runs in ns (nil if it does not
// exist), so the wizard can offer a migration for any app name.
func (p *Platform) InspectNamespace(ctx context.Context, ns string) (*Migration, error) {
	return p.inspectNamespace(ctx, ns)
}

// inspectNamespace reports what already runs in ns, or nil if it does not exist.
func (p *Platform) inspectNamespace(ctx context.Context, ns string) (*Migration, error) {
	var n corev1.Namespace
	if err := p.Kube.Get(ctx, client.ObjectKey{Name: ns}, &n); apierrors.IsNotFound(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	m := &Migration{Namespace: ns, Managed: n.Labels[render.LabelManagedBy] == render.ManagedBy}
	var deps appsv1.DeploymentList
	if err := p.Kube.List(ctx, &deps, client.InNamespace(ns)); err != nil {
		return nil, err
	}
	for _, d := range deps.Items {
		img := ""
		if cs := d.Spec.Template.Spec.Containers; len(cs) > 0 {
			img = cs[0].Image
		}
		m.Deployments = append(m.Deployments, fmt.Sprintf("%s (%s)", d.Name, img))
		if m.ArgoCD == "" {
			m.ArgoCD = argoApp(d.ObjectMeta)
		}
	}
	var ings networkingv1.IngressList
	if err := p.Kube.List(ctx, &ings, client.InNamespace(ns)); err != nil {
		return nil, err
	}
	for _, ing := range ings.Items {
		for _, r := range ing.Spec.Rules {
			m.Hosts = append(m.Hosts, r.Host)
		}
		if m.ArgoCD == "" {
			m.ArgoCD = argoApp(ing.ObjectMeta)
		}
	}
	var secs corev1.SecretList
	if err := p.Kube.List(ctx, &secs, client.InNamespace(ns)); err != nil {
		return nil, err
	}
	for _, s := range secs.Items {
		if s.Type == corev1.SecretTypeOpaque {
			m.Secrets = append(m.Secrets, s.Name)
		}
	}
	return m, nil
}

// Propose inspects a repo and proposes how to deploy it: its rendimiento.yaml if it has one,
// otherwise a generated one, plus what is already running in the namespace it would use.
func (p *Platform) Propose(ctx context.Context, installation int64, repo, branch string) (*Proposal, error) {
	fsys, err := p.GitHub.RepoFS(ctx, installation, repo, branch)
	if err != nil {
		return nil, err
	}
	results, err := detect.Detect(fsys)
	if err != nil {
		return nil, err
	}
	prop := &Proposal{Repo: repo, DefaultBranch: branch, Detected: results, Files: map[string]string{}, Railpack: p.Config.Railpack}
	if raw, err := fs.ReadFile(fsys, spec.FileName); err == nil {
		s, err := spec.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("the repo's %s is invalid: %w", spec.FileName, err)
		}
		prop.Spec, prop.Existing = *s, true
	}
	name := repo[strings.LastIndex(repo, "/")+1:]
	if prop.Migration, err = p.inspectNamespace(ctx, generate.Slug(name)); err != nil {
		return nil, err
	}
	if prop.Migration == nil && prop.Existing {
		// The repo's hosts may already be served from a namespace named
		// differently from the repo: suggest migrating that one.
		if ns, err := p.namespaceServing(ctx, prop.Spec); err == nil && ns != "" {
			if prop.Migration, err = p.inspectNamespace(ctx, ns); err != nil {
				return nil, err
			}
			prop.SuggestedName = ns
		}
	}
	if prop.Existing {
		return prop, nil
	}
	plan, err := p.Generator.Generate(ctx, generate.Input{RepoName: name, Zone: p.Config.Zone, FS: fsys, Results: results})
	if err != nil {
		return nil, err
	}
	prop.Spec, prop.Files = plan.Spec, plan.Files
	return prop, nil
}

// OnboardRequest is what the New app wizard submits.
type OnboardRequest struct {
	Installation  int64             `json:"installation"`
	Repo          string            `json:"repo"`
	DefaultBranch string            `json:"defaultBranch"`
	Name          string            `json:"name"`
	Spec          spec.Spec         `json:"spec"`
	Files         map[string]string `json:"files"` // generated files the user kept
	// Adopt migrates an app already running in the namespace (see Migration).
	Adopt bool `json:"adopt"`
}

// OnboardResult is the created app and either the onboarding PR or the first run.
type OnboardResult struct {
	App *store.App      `json:"app"`
	PR  *gh.PullRequest `json:"pr,omitempty"`
	Run *store.Run      `json:"run,omitempty"`
}

// Onboard registers the app, creates its App object (waiting for a build)
// and either opens the onboarding PR or, if the repo already carries a
// rendimiento.yaml, queues the first build right away.
func (p *Platform) Onboard(ctx context.Context, req OnboardRequest, existing bool) (*OnboardResult, error) {
	req.Spec.Default()
	if err := req.Spec.Validate(); err != nil {
		return nil, err
	}
	req.Name = generate.Slug(req.Name)
	app := &store.App{Name: req.Name, Repo: req.Repo, InstallationID: req.Installation, DefaultBranch: req.DefaultBranch, Spec: req.Spec}
	if err := p.Store.CreateApp(ctx, app); err != nil {
		if store.IsUniqueViolation(err) {
			return nil, fmt.Errorf("an app named %q already exists", req.Name)
		}
		return nil, err
	}
	cr := &v1alpha1.App{
		ObjectMeta: metav1.ObjectMeta{Name: app.Name, Labels: map[string]string{"rendimiento.ai/app-id": strconv.FormatInt(app.ID, 10)}},
		Spec: v1alpha1.AppSpec{Repo: app.Repo, Services: app.Spec.Services, Jobs: app.Spec.Jobs, SharedNamespace: app.Spec.SharedNamespace,
			Postgres: app.Spec.Postgres, Redis: app.Spec.Redis, Adopt: req.Adopt},
	}
	if err := p.Kube.Create(ctx, cr); err != nil {
		_ = p.Store.DeleteApp(ctx, app.ID)
		return nil, fmt.Errorf("create App object: %w", err)
	}
	res := &OnboardResult{App: app}
	if existing {
		run, err := p.QueueRun(ctx, app, req.DefaultBranch, "", "manual")
		if err != nil {
			return nil, err
		}
		res.Run = run
		return res, nil
	}
	raw, err := req.Spec.Marshal()
	if err != nil {
		return nil, err
	}
	files := map[string]string{spec.FileName: "# Generated by rendimiento.ai — edit freely; changes deploy on merge.\n" + string(raw)}
	for path, content := range req.Files {
		files[path] = content
	}
	body := fmt.Sprintf("This PR connects **%s** to rendimiento.ai.\n\nMerging it builds the app and deploys it to:\n", req.Repo)
	for _, s := range req.Spec.Services {
		if s.Domain != "" {
			body += fmt.Sprintf("- `%s` → https://%s\n", s.Name, s.Domain)
		}
	}
	body += "\nThe build runs on this PR first, so you can see it pass before merging."
	pr, err := p.GitHub.OpenPR(ctx, req.Installation, req.Repo, req.DefaultBranch, "rendimiento/onboard", "Deploy with rendimiento.ai", body, files)
	if err != nil {
		return nil, fmt.Errorf("app registered, but opening the PR failed: %w", err)
	}
	res.PR = pr
	return res, nil
}

// DeleteApp removes the App object (the controller then removes DNS and,
// through owner references, the namespace) and the app's records.
func (p *Platform) DeleteApp(ctx context.Context, app *store.App) error {
	err := p.Kube.Delete(ctx, &v1alpha1.App{ObjectMeta: metav1.ObjectMeta{Name: app.Name}})
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return p.Store.DeleteApp(ctx, app.ID)
}

// Impact describes what deleting an app would destroy.
type Impact struct {
	Namespace string   `json:"namespace"`
	Volumes   []string `json:"volumes"` // "name (size)"
	Secrets   []string `json:"secrets"`
	Adopted   bool     `json:"adopted"` // it ran before rendimiento took it over
	// SharedNamespace: the namespace and whatever else lives in it are kept.
	SharedNamespace bool `json:"sharedNamespace"`
}

// DeleteImpact lists what deleting an app would remove (namespace, volumes, secrets), shown before
// deleting.
func (p *Platform) DeleteImpact(ctx context.Context, app *store.App) (*Impact, error) {
	im := &Impact{Namespace: app.Name, Volumes: []string{}, Secrets: []string{}}
	var cr v1alpha1.App
	if err := p.Kube.Get(ctx, client.ObjectKey{Name: app.Name}, &cr); err == nil {
		im.Adopted = cr.Spec.Adopt
		im.SharedNamespace = cr.Spec.SharedNamespace
	}
	own := func(o metav1.Object) bool {
		// With a shared namespace only objects rendimiento owns are deleted.
		if !im.SharedNamespace {
			return true
		}
		for _, r := range o.GetOwnerReferences() {
			if r.UID == cr.UID {
				return true
			}
		}
		return false
	}
	var pvcs corev1.PersistentVolumeClaimList
	if err := p.Kube.List(ctx, &pvcs, client.InNamespace(app.Name)); err != nil {
		return nil, err
	}
	for _, v := range pvcs.Items {
		if !own(&v) {
			continue
		}
		size := v.Spec.Resources.Requests[corev1.ResourceStorage]
		im.Volumes = append(im.Volumes, fmt.Sprintf("%s (%s)", v.Name, size.String()))
	}
	var secs corev1.SecretList
	if err := p.Kube.List(ctx, &secs, client.InNamespace(app.Name)); err != nil {
		return nil, err
	}
	for _, sec := range secs.Items {
		if sec.Type == corev1.SecretTypeOpaque && own(&sec) {
			im.Secrets = append(im.Secrets, sec.Name)
		}
	}
	return im, nil
}

// DisconnectApp stops managing an app but leaves everything running: the
// namespace and its objects lose rendimiento's ownership (so deleting the
// App garbage-collects nothing) and labels, DNS records stay, and the App
// and its history are removed. It is the reverse of a migration: the app
// can be migrated back later, or managed some other way.
func (p *Platform) DisconnectApp(ctx context.Context, app *store.App) error {
	var cr v1alpha1.App
	err := p.Kube.Get(ctx, client.ObjectKey{Name: app.Name}, &cr)
	if apierrors.IsNotFound(err) {
		return p.Store.DeleteApp(ctx, app.ID)
	}
	if err != nil {
		return err
	}
	// 1. Stop reconciling, and mark the App so its deletion keeps DNS.
	patch := client.MergeFrom(cr.DeepCopy())
	cr.Spec.Suspend = true
	if cr.Annotations == nil {
		cr.Annotations = map[string]string{}
	}
	cr.Annotations[controller.AnnotationDisconnect] = "true"
	if err := p.Kube.Patch(ctx, &cr, patch); err != nil {
		return fmt.Errorf("suspend: %w", err)
	}
	// 2. Release every object: drop the owner reference and rendimiento's
	// labels (metadata only, so no pod restarts).
	var ns corev1.Namespace
	objs := []client.Object{}
	if err := p.Kube.Get(ctx, client.ObjectKey{Name: app.Name}, &ns); err == nil {
		objs = append(objs, &ns)
	}
	lists := []client.ObjectList{&appsv1.DeploymentList{}, &corev1.ServiceList{}, &networkingv1.IngressList{},
		&corev1.PersistentVolumeClaimList{}, &batchv1.CronJobList{}, &corev1.SecretList{}}
	for _, l := range lists {
		if err := p.Kube.List(ctx, l, client.InNamespace(app.Name)); err != nil {
			return err
		}
		items, err := meta.ExtractList(l)
		if err != nil {
			return err
		}
		for _, it := range items {
			objs = append(objs, it.(client.Object))
		}
	}
	for _, o := range objs {
		if err := release(ctx, p.Kube, o, cr.UID); err != nil {
			return fmt.Errorf("release %T %s: %w", o, o.GetName(), err)
		}
	}
	// 3. Delete the App; nothing is owned by it any more.
	if err := p.Kube.Delete(ctx, &cr); client.IgnoreNotFound(err) != nil {
		return err
	}
	p.Log.Info("app disconnected; its workloads keep running unmanaged", "app", app.Name, "objects", len(objs))
	return p.Store.DeleteApp(ctx, app.ID)
}

// release removes owner references to the App and rendimiento's top-level
// labels from one object.
func release(ctx context.Context, c client.Client, o client.Object, owner types.UID) error {
	// Snapshot first: GetLabels returns the object's own map, so editing it
	// before taking the patch base would hide the change from the patch.
	patch := client.MergeFrom(o.DeepCopyObject().(client.Object))
	var refs []metav1.OwnerReference
	changed := false
	for _, r := range o.GetOwnerReferences() {
		if r.UID == owner {
			changed = true
			continue
		}
		refs = append(refs, r)
	}
	labels := o.GetLabels()
	if labels[render.LabelManagedBy] == render.ManagedBy {
		for _, k := range []string{render.LabelManagedBy, render.LabelApp, render.LabelService} {
			delete(labels, k)
		}
		changed = true
	}
	if !changed {
		return nil
	}
	o.SetOwnerReferences(refs)
	o.SetLabels(labels)
	return c.Patch(ctx, o, patch)
}

// ---- webhooks ----

// PushEvent is the part of a GitHub push webhook the platform uses.
type PushEvent struct {
	Installation int64
	Repo         string
	Branch       string
	SHA          string
	Deleted      bool
}

// HandlePush queues a run for every app deployed from the repo. Pushes to
// the app's default branch deploy; other branches only build.
func (p *Platform) HandlePush(ctx context.Context, e PushEvent) ([]*store.Run, error) {
	if e.Deleted || e.SHA == "" || strings.Trim(e.SHA, "0") == "" {
		return nil, nil
	}
	apps, err := p.Store.AppsForRepo(ctx, e.Repo)
	if err != nil {
		return nil, err
	}
	var runs []*store.Run
	for _, app := range apps {
		event := "push"
		if e.Branch != app.DefaultBranch {
			event = "branch"
		}
		run, err := p.QueueRun(ctx, app, e.Branch, e.SHA, event)
		if err != nil {
			p.Log.Warn("could not queue run", "app", app.Name, "err", err)
			continue
		}
		if run != nil {
			runs = append(runs, run)
		}
	}
	return runs, nil
}

// QueueRun reads rendimiento.yaml at the commit, plans the steps and queues
// the run. An empty sha means "the branch head" (manual runs).
func (p *Platform) QueueRun(ctx context.Context, app *store.App, branch, sha, event string) (*store.Run, error) {
	if sha == "" {
		// Manual runs build the branch head: pin it to a commit so the image,
		// the GitHub check and change detection all refer to a real SHA.
		if head, err := p.GitHub.BranchSHA(ctx, app.InstallationID, app.Repo, branch); err == nil {
			sha = head
		}
	}
	ref := sha
	if ref == "" {
		ref = branch
	}
	raw, err := p.GitHub.FileAt(ctx, app.InstallationID, app.Repo, spec.FileName, ref)
	if gh.IsNotFound(err) {
		if event == "manual" {
			return nil, fmt.Errorf("%s is not on %s yet; merge the onboarding PR first", spec.FileName, branch)
		}
		return nil, nil // repo not onboarded on this branch; ignore
	}
	if err != nil {
		return nil, err
	}
	sp, err := spec.Parse(raw)
	if err != nil {
		err = fmt.Errorf("%s at %s: %w", spec.FileName, shortSHA(ref), err)
		if sha == "" {
			return nil, err // no commit to record the failure against
		}
		return p.rejectRun(ctx, app, branch, sha, event, err)
	}
	if sha == "" {
		sha = branch
	} else {
		// A good rendimiento.yaml on this branch fixes any earlier rejection.
		if _, err := p.Store.ResolveProblems(ctx, store.ProblemMatch{Message: msgSpecRejected, App: app.Name, Attr: "branch=" + branch},
			"fixed by "+shortSHA(sha)); err != nil {
			p.Log.Warn("could not resolve problems", "app", app.Name, "err", err)
		}
	}
	if err := p.Store.CancelQueued(ctx, app.ID, branch); err != nil {
		return nil, err
	}
	deploy := branch == app.DefaultBranch
	if deploy {
		if err := p.Store.UpdateAppSpec(ctx, app.ID, *sp); err != nil {
			return nil, err
		}
		app.Spec = *sp
	}
	run := &store.Run{AppID: app.ID, SHA: sha, Branch: branch, Event: event, Deploy: deploy}
	if err := p.Store.CreateRun(ctx, run, pipeline.Plan(app.Name, p.Config.Registry, *sp)); err != nil {
		return nil, err
	}
	p.Hub.Publish(appTopic(app.Name), events.Event{Type: "run", Data: run})
	return run, nil
}

// msgSpecRejected is the problem a rejected rendimiento.yaml records; the
// next push accepted on the same branch resolves it.
const msgSpecRejected = "rendimiento.yaml rejected; the push was not built"

// rejectRun records a push whose rendimiento.yaml is invalid as a failed
// run, so it shows where pushes are looked for: on the app page, as a red
// check on the commit in GitHub and, for the default branch, in an email.
// Nothing was built or released.
func (p *Platform) rejectRun(ctx context.Context, app *store.App, branch, sha, event string, cause error) (*store.Run, error) {
	run := &store.Run{AppID: app.ID, SHA: sha, Branch: branch, Event: event, Deploy: branch == app.DefaultBranch}
	if err := p.Store.CreateRejectedRun(ctx, run, cause.Error()); err != nil {
		return nil, err
	}
	p.Log.Warn(msgSpecRejected, "app", app.Name, "run", run.ID, "branch", branch, "err", cause)
	p.Hub.Publish(appTopic(app.Name), events.Event{Type: "run", Data: run})
	p.finishCheck(ctx, app, run, p.startCheck(ctx, app, run), store.RunFailed, cause.Error())
	if run.Deploy {
		p.Notify.Notify(notify.Message{
			Tone: notify.Critical, Key: fmt.Sprintf("spec:%s:%s", app.Name, sha),
			Subject: fmt.Sprintf("✗ %s: rendimiento.yaml has an error", app.Name),
			Title:   "The push was not deployed",
			Summary: fmt.Sprintf("rendimiento.yaml on %s has an error, so nothing was built or released; the app keeps running its current release. Fix the file and push again.", branch),
			Facts: []notify.Fact{
				{Label: "App", Value: app.Name},
				{Label: "Commit", Value: fmt.Sprintf("%s on %s", shortSHA(sha), branch)},
				{Label: "Run", Value: fmt.Sprintf("#%d", run.ID)},
			},
			Details:   cause.Error(),
			ActionURL: fmt.Sprintf("%s/apps/%s/runs/%d", p.Config.BaseURL, app.Name, run.ID), ActionLabel: "Open the run",
		})
	}
	return run, nil
}

// ---- worker ----

// Work claims queued runs until ctx ends. Several workers may run; the
// pipeline Runner enforces the global step concurrency limit.
func (p *Platform) Work(ctx context.Context) {
	for ctx.Err() == nil {
		run, err := p.Store.ClaimRun(ctx)
		if errors.Is(err, store.ErrNotFound) {
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
			}
			continue
		}
		if err != nil {
			p.Log.Error("claim run", "err", err)
			time.Sleep(5 * time.Second)
			continue
		}
		p.execute(ctx, run)
	}
}

// Cancel stops a run in progress in this process; it reports whether one was found.
func (p *Platform) Cancel(runID int64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.cancels[runID]; ok {
		c()
		return true
	}
	return false
}

func (p *Platform) execute(ctx context.Context, run *store.Run) {
	log := p.Log.With("run", run.ID)
	app, err := p.Store.GetAppByID(ctx, run.AppID)
	if err != nil {
		_ = p.Store.FinishRun(ctx, run.ID, store.RunFailed, "app no longer exists")
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	p.mu.Lock()
	if p.cancels == nil {
		p.cancels = map[int64]context.CancelFunc{}
	}
	p.cancels[run.ID] = cancel
	p.mu.Unlock()
	defer func() {
		cancel()
		p.mu.Lock()
		delete(p.cancels, run.ID)
		p.mu.Unlock()
	}()

	checkID := p.startCheck(ctx, app, run)
	p.publishRun(app, run.ID)

	sp := app.Spec
	if !run.Deploy {
		// Branch/PR runs build the spec at their own commit.
		if raw, err := p.GitHub.FileAt(ctx, app.InstallationID, app.Repo, spec.FileName, run.SHA); err == nil {
			if s, err := spec.Parse(raw); err == nil {
				sp = *s
			}
		}
	}
	steps := pipeline.Plan(app.Name, p.Config.Registry, sp)
	files, prev := p.changes(ctx, app, run)
	reuse, reuseNote := reusable(sp, files, prev)
	skip := skippedTasks(sp, run.Deploy, files, prev)
	var toRun []pipeline.Step
	for _, st := range steps {
		if img, ok := reuse[st.Service]; ok && img != "" {
			p.StepUpdate(strconv.FormatInt(run.ID, 10), st.ID, pipeline.StepResult{
				Status: pipeline.StatusReused, Message: reuseNote, Started: time.Now(), Finished: time.Now()})
			continue
		}
		if why, ok := skip[st.ID]; ok {
			p.StepUpdate(strconv.FormatInt(run.ID, 10), st.ID, pipeline.StepResult{
				Status: pipeline.StatusSkipped, Message: why, Started: time.Now(), Finished: time.Now()})
			continue
		}
		toRun = append(toRun, st)
	}
	// A step whose dependency did not run this time (its image was reused,
	// or a task was skipped) does not wait for it.
	planned := map[string]bool{}
	for _, st := range toRun {
		planned[st.ID] = true
	}
	for i := range toRun {
		var deps []string
		for _, d := range toRun[i].DependsOn {
			if planned[d] {
				deps = append(deps, d)
			}
		}
		toRun[i].DependsOn = deps
	}
	src := pipeline.Source{Repo: app.Repo, SHA: run.SHA, Branch: run.Branch, Deploy: run.Deploy}
	results, err := p.Runner.Run(withInstallation(runCtx, app.InstallationID), strconv.FormatInt(run.ID, 10), src, toRun)

	status, msg := store.RunSucceeded, ""
	switch {
	case err != nil:
		status, msg = store.RunFailed, err.Error()
	case runCtx.Err() != nil && ctx.Err() == nil:
		status, msg = store.RunCancelled, "cancelled"
	default:
		for _, s := range toRun {
			if r := results[s.ID]; r.Status != pipeline.StatusSucceeded && !s.Optional {
				status, msg = store.RunFailed, fmt.Sprintf("%s %s", s.ID, r.Status)
				break
			}
		}
	}
	if status == store.RunSucceeded && run.Deploy {
		rel, err := p.release(ctx, app, run, sp, toRun, results, reuse)
		var blocked errPreDeploy
		switch {
		case errors.As(err, &blocked):
			status, msg = store.RunFailed, fmt.Sprintf("release #%d not deployed: %s", rel.Number, blocked.reason)
		case err != nil:
			status, msg = store.RunFailed, "release failed: "+err.Error()
		default:
			msg = fmt.Sprintf("released #%d", rel.Number)
			p.startVerification(ctx, app, rel)
		}
	}
	if err := p.Store.FinishRun(ctx, run.ID, status, msg); err != nil {
		log.Error("finish run", "err", err)
	}
	if status == store.RunFailed && run.Deploy {
		p.notifyRunFailed(app, run, msg, toRun, results)
	}
	p.finishCheck(ctx, app, run, checkID, status, msg)
	p.publishRun(app, run.ID)
	log.Info("run finished", "app", app.Name, "status", status, "message", msg)
}

func (p *Platform) release(ctx context.Context, app *store.App, run *store.Run, sp spec.Spec, steps []pipeline.Step, results map[string]pipeline.StepResult, reuse map[string]string) (*store.Release, error) {
	images := map[string]string{}
	for key, img := range reuse {
		images[key] = img // unchanged since the last release
	}
	for _, s := range steps {
		if s.Kind == pipeline.KindBuild {
			images[s.Service] = s.Target + "@" + results[s.ID].Digest
		}
	}
	for _, svc := range sp.Services {
		if svc.Image != "" {
			images[svc.Name] = svc.Image // ready-made image, released as given
		}
	}
	_, prevErr := p.Store.LatestRelease(ctx, app.ID)
	first := errors.Is(prevErr, store.ErrNotFound)
	rel := &store.Release{AppID: app.ID, RunID: &run.ID, SHA: run.SHA, Images: images, Spec: sp}
	if err := p.Store.CreateRelease(ctx, rel); err != nil {
		return nil, err
	}
	if err := p.preDeploy(ctx, app, rel, first); err != nil {
		return rel, err
	}
	return rel, p.pointApp(ctx, app.Name, rel)
}

// errPreDeploy means a release was recorded but not deployed: a required
// pre-deploy task failed.
type errPreDeploy struct{ reason string }

func (e errPreDeploy) Error() string { return e.reason }

// preDeploy runs the release's pre-deploy tasks before it is rolled out. A
// failed required task stops the release (recorded as blocked); the app
// keeps running its current release. On an app's first release there is no
// environment yet (namespace, secrets, databases), so they are skipped.
func (p *Platform) preDeploy(ctx context.Context, app *store.App, rel *store.Release, first bool) error {
	tasks := stageTasks(rel, spec.StagePreDeploy)
	if len(tasks) == 0 {
		return nil
	}
	if first {
		for _, t := range tasks {
			p.saveTask(store.ReleaseTask{ReleaseID: rel.ID, Name: t.Name, Stage: spec.StagePreDeploy, Optional: t.Optional, Status: store.TaskSkipped,
				Message: "the app's first release: its environment does not exist yet; pre-deploy tasks run from the next release"})
		}
		return nil
	}
	results := p.runStage(ctx, app, rel, spec.StagePreDeploy)
	failures, _ := judgeTasks(results)
	if len(failures) == 0 {
		return nil
	}
	reason := strings.Join(failures, "; ")
	p.setVerification(app, rel, store.VerifyBlocked, "not deployed: "+reason)
	return errPreDeploy{reason: reason}
}

var fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// changes lists the files changed since the latest release, for a push to
// the default branch. A nil release means "unknown": run everything. That
// covers manual and branch runs, no earlier release, a failed or truncated
// diff, and a change to rendimiento.yaml.
func (p *Platform) changes(ctx context.Context, app *store.App, run *store.Run) ([]string, *store.Release) {
	if !run.Deploy || run.Event != "push" || !fullSHA.MatchString(run.SHA) {
		return nil, nil
	}
	prev, err := p.Store.LatestRelease(ctx, app.ID)
	if err != nil || !fullSHA.MatchString(prev.SHA) || prev.SHA == run.SHA {
		return nil, nil
	}
	files, complete, err := p.GitHub.ChangedFiles(ctx, app.InstallationID, app.Repo, prev.SHA, run.SHA)
	if err != nil {
		p.Log.Warn("change detection failed; building everything", "app", app.Name, "err", err)
		return nil, nil
	}
	if !complete {
		return nil, nil
	}
	for _, f := range files {
		if f == spec.FileName {
			return nil, nil
		}
	}
	return files, prev
}

// touched reports whether any of files is under dir or a watched path.
func touched(files []string, dir string, watch []string) bool {
	for _, f := range files {
		if spec.Touches(f, dir, watch) {
			return true
		}
	}
	return false
}

// reusable decides which built services and jobs can keep the image from
// the latest release because nothing they are built from changed since.
// It returns image key → image, and a note for the skipped steps.
func reusable(sp spec.Spec, files []string, prev *store.Release) (map[string]string, string) {
	if prev == nil {
		return nil, ""
	}
	reuse := map[string]string{}
	for _, svc := range sp.Services {
		if svc.Image == "" && prev.Images[svc.Name] != "" && !touched(files, svc.Path, svc.Watch) {
			reuse[svc.Name] = prev.Images[svc.Name]
		}
	}
	for _, j := range sp.Jobs {
		if j.Path != "" && prev.Images[j.ImageKey()] != "" && !touched(files, j.Path, j.Watch) {
			reuse[j.ImageKey()] = prev.Images[j.ImageKey()]
		}
	}
	return reuse, fmt.Sprintf("unchanged since release #%d; its image is reused", prev.Number)
}

// skippedTasks returns the task steps that do not run this time, with why:
// deploy-only tasks on branch and pull request runs (they may read
// secrets), and tasks whose path and watch paths are unchanged since the
// latest release.
func skippedTasks(sp spec.Spec, deploy bool, files []string, prev *store.Release) map[string]string {
	skip := map[string]string{}
	for _, t := range sp.Tasks {
		switch {
		case t.Deploys():
			continue // not a CI step
		case t.When == spec.TaskOnDeploy && !deploy:
			skip[pipeline.TaskStepID(t.Name)] = "runs only for pushes to the default branch (when: deploy)"
		case prev != nil && !touched(files, t.Path, t.Watch):
			skip[pipeline.TaskStepID(t.Name)] = fmt.Sprintf("nothing under %s changed since release #%d", strings.Join(append([]string{t.Path}, t.Watch...), ", "), prev.Number)
		}
	}
	return skip
}

// Rollback creates a new release that restores an earlier one's images and
// spec (the Rollback button). It ends any verification of the app.
func (p *Platform) Rollback(ctx context.Context, app *store.App, number int64) (*store.Release, error) {
	old, err := p.Store.GetRelease(ctx, app.ID, number)
	if err != nil {
		return nil, err
	}
	if old.VerifyStatus == store.VerifyBlocked {
		return nil, fmt.Errorf("release #%d was never deployed (a pre-deploy task failed), so it cannot be rolled back to", number)
	}
	p.stopVerification(app.ID)
	return p.rollbackTo(ctx, app, old, "rolled back by hand")
}

// rollbackTo releases an earlier release's images and spec again, as a new
// release whose verification is recorded as skipped with note.
func (p *Platform) rollbackTo(ctx context.Context, app *store.App, old *store.Release, note string) (*store.Release, error) {
	rel := &store.Release{AppID: app.ID, SHA: old.SHA, Images: old.Images, Spec: old.Spec, RollbackOf: &old.Number}
	if err := p.Store.CreateRelease(ctx, rel); err != nil {
		return nil, err
	}
	if err := p.pointApp(ctx, app.Name, rel); err != nil {
		return nil, err
	}
	p.setVerification(app, rel, store.VerifySkipped, note)
	return rel, nil
}

// pointApp writes the release into the App object; the controller does the rest.
func (p *Platform) pointApp(ctx context.Context, name string, rel *store.Release) error {
	for attempt := 0; ; attempt++ {
		var cr v1alpha1.App
		if err := p.Kube.Get(ctx, client.ObjectKey{Name: name}, &cr); err != nil {
			return err
		}
		cr.Spec.Services = rel.Spec.Services
		cr.Spec.Jobs = rel.Spec.Jobs
		cr.Spec.SharedNamespace = rel.Spec.SharedNamespace
		cr.Spec.Postgres, cr.Spec.Redis = rel.Spec.Postgres, rel.Spec.Redis
		cr.Spec.Images = rel.Images
		cr.Spec.Release = rel.Number
		err := p.Kube.Update(ctx, &cr)
		if apierrors.IsConflict(err) && attempt < 5 {
			continue
		}
		if err == nil {
			p.Hub.Publish(appTopic(name), events.Event{Type: "release", Data: rel})
		}
		return err
	}
}

// ---- GitHub check runs ----

func (p *Platform) startCheck(ctx context.Context, app *store.App, run *store.Run) int64 {
	if len(run.SHA) != 40 {
		return 0 // manual runs on a branch name have no commit to attach to
	}
	id, err := p.GitHub.CreateCheck(ctx, app.InstallationID, app.Repo, gh.CheckRun{
		Name: "rendimiento: " + app.Name, HeadSHA: run.SHA, Status: "in_progress",
		DetailsURL: fmt.Sprintf("%s/apps/%s/runs/%d", p.Config.BaseURL, app.Name, run.ID),
	})
	if err != nil {
		p.Log.Warn("create check run", "err", err)
		return 0
	}
	_ = p.Store.SetCheckRun(ctx, run.ID, id)
	return id
}

func (p *Platform) finishCheck(ctx context.Context, app *store.App, run *store.Run, id int64, status, msg string) {
	if id == 0 {
		return
	}
	conclusion := map[string]string{store.RunSucceeded: "success", store.RunFailed: "failure", store.RunCancelled: "cancelled"}[status]
	title := "Build " + status
	if msg != "" {
		title += ": " + msg
	}
	if err := p.GitHub.UpdateCheck(ctx, app.InstallationID, app.Repo, id, gh.CheckRun{
		Status: "completed", Conclusion: conclusion, Output: gh.CheckOutput(title, fmt.Sprintf("[View run](%s/apps/%s/runs/%d)", p.Config.BaseURL, app.Name, run.ID)),
	}); err != nil {
		p.Log.Warn("update check run", "err", err)
	}
}

// ---- events & recorder ----

func appTopic(app string) string { return "app/" + app }

// RunTopic is the events topic for a run's live updates.
func RunTopic(runID int64) string { return "run/" + strconv.FormatInt(runID, 10) }

// AppTopic is the events topic for an app's live updates.
func AppTopic(app string) string { return appTopic(app) }

func (p *Platform) publishRun(app *store.App, runID int64) {
	if r, err := p.Store.GetRun(context.Background(), runID); err == nil {
		p.Hub.Publish(RunTopic(runID), events.Event{Type: "run", Data: r})
		p.Hub.Publish(appTopic(app.Name), events.Event{Type: "run", Data: r})
	}
}

type installationKey struct{}

func withInstallation(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, installationKey{}, id)
}

// CloneToken is plugged into the executor: it mints a token for the
// installation carried in the run's context.
func (p *Platform) CloneToken(ctx context.Context, _ string) (string, error) {
	id, ok := ctx.Value(installationKey{}).(int64)
	if !ok || id == 0 {
		return "", nil
	}
	return p.GitHub.CloneToken(ctx, id)
}
