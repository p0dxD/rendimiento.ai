package addon

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/p0dxD/rendimiento.ai/api/v1alpha1"
	gh "github.com/p0dxD/rendimiento.ai/internal/github"
)

// Dir is the folder of the gitops repository holding add-on definitions.
const Dir = "addons"

const (
	// LabelGitops marks Addon objects created from a definition in git.
	LabelGitops = "rendimiento.ai/gitops"
	// AnnotationSource is the definition's path and commit.
	AnnotationSource = "rendimiento.ai/source"
)

// Definition is one add-on as written in addons/<name>.yaml.
type Definition struct {
	Name            string               `json:"name"`
	Title           string               `json:"title,omitempty"`
	Category        string               `json:"category,omitempty"`
	Description     string               `json:"description,omitempty"`
	Namespace       string               `json:"namespace"`
	CreateNamespace bool                 `json:"createNamespace,omitempty"`
	Helm            *v1alpha1.HelmSource `json:"helm,omitempty"`
	// Git renders a folder; Repo defaults to the gitops repository itself.
	Git         *v1alpha1.GitSource `json:"git,omitempty"`
	ReleaseName string              `json:"releaseName,omitempty"`
	// Values are Helm values, as YAML under this key.
	Values            map[string]any `json:"values,omitempty"`
	Adopt             bool           `json:"adopt,omitempty"`
	AllowAdoptChanges bool           `json:"allowAdoptChanges,omitempty"`
	Prune             bool           `json:"prune,omitempty"`
	ManualSync        bool           `json:"manualSync,omitempty"`
	Suspend           bool           `json:"suspend,omitempty"`
}

var dnsName = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

// ParseDefinition reads and validates one addons/<name>.yaml; unknown fields are errors.
func ParseDefinition(data []byte) (*Definition, error) {
	var d Definition
	if err := yaml.UnmarshalStrict(data, &d); err != nil {
		return nil, err
	}
	return &d, d.Validate()
}

// Validate checks names and that exactly one source (helm or git) is set.
func (d *Definition) Validate() error {
	var errs []error
	if !dnsName.MatchString(d.Name) {
		errs = append(errs, fmt.Errorf("name %q must be a lowercase DNS label", d.Name))
	}
	if !dnsName.MatchString(d.Namespace) {
		errs = append(errs, fmt.Errorf("namespace %q must be a lowercase DNS label", d.Namespace))
	}
	switch {
	case (d.Helm == nil) == (d.Git == nil):
		errs = append(errs, errors.New("set exactly one of helm or git"))
	case d.Helm != nil && (d.Helm.Repo == "" || d.Helm.Chart == "" || d.Helm.Version == ""):
		errs = append(errs, errors.New("helm needs repo, chart and version"))
	case d.Git != nil && (d.Git.Path == "" || strings.Contains(d.Git.Path, "..")):
		errs = append(errs, errors.New("git needs a path inside the repository"))
	}
	if d.Git != nil && len(d.Values) > 0 {
		errs = append(errs, errors.New("values only apply to helm sources"))
	}
	return errors.Join(errs...)
}

// Marshal writes the definition as it is stored in git.
func (d *Definition) Marshal() ([]byte, error) {
	b, err := yaml.Marshal(d)
	if err != nil {
		return nil, err
	}
	return append([]byte("# rendimiento add-on. Edit here or on the Add-ons page; pushes to the default branch sync it.\n"), b...), nil
}

// Spec is the Addon spec the definition asks for. gitopsRepo and sha are
// the repository and commit it was read from.
func (d *Definition) Spec(gitopsRepo, sha string) (v1alpha1.AddonSpec, error) {
	s := v1alpha1.AddonSpec{
		Namespace: d.Namespace, CreateNamespace: d.CreateNamespace, ReleaseName: d.ReleaseName,
		Adopt: d.Adopt, AllowAdoptChanges: d.AllowAdoptChanges, Prune: d.Prune, ManualSync: d.ManualSync, Suspend: d.Suspend,
		Title: d.Title, Category: d.Category, Description: d.Description,
	}
	if d.Helm != nil {
		h := *d.Helm
		s.Source.Helm = &h
		if len(d.Values) > 0 {
			b, err := yaml.Marshal(d.Values)
			if err != nil {
				return s, err
			}
			s.Values = string(b)
		}
	}
	if d.Git != nil {
		g := *d.Git
		if g.Repo == "" || strings.EqualFold(g.Repo, gitopsRepo) {
			// A folder of the gitops repo itself: render the same commit
			// the definition came from, so both change together.
			g.Repo, g.Revision = gitopsRepo, sha
		}
		s.Source.Git = &g
	}
	return s, nil
}

// ---- reading git through the GitHub App ----

// GitHubFetcher reads repositories through the GitHub App.
type GitHubFetcher struct{ GitHub *gh.Holder }

var fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Fetch implements GitFetcher through the GitHub App installation that owns repo.
func (f GitHubFetcher) Fetch(ctx context.Context, repo, dir, ref string) (fs.FS, string, error) {
	owner, _, _ := strings.Cut(repo, "/")
	c, inst, err := f.GitHub.ForOwner(ctx, owner)
	if err != nil {
		return nil, "", err
	}
	sha := ref
	if !fullSHA.MatchString(ref) {
		if ref == "" {
			r, err := c.Repo(ctx, repo)
			if err != nil {
				return nil, "", err
			}
			ref = r.DefaultBranch
		}
		if sha, err = c.BranchSHA(ctx, repo, ref); err != nil {
			return nil, "", err
		}
	}
	root, err := f.GitHub.RepoFS(ctx, inst, repo, sha)
	if err != nil {
		return nil, "", err
	}
	sub, err := fs.Sub(root, strings.Trim(path.Clean(dir), "/"))
	if err != nil {
		return nil, "", err
	}
	return sub, sha, nil
}

// ---- syncing definitions into the cluster ----

// Syncer makes the Addon objects match the definitions in the gitops repo.
type Syncer struct {
	Kube   client.Client
	GitHub *gh.Holder
	Repo   string // owner/name, e.g. p0dxD/gitops
	Log    *slog.Logger

	trigger chan struct{}
	once    sync.Once
	mu      sync.Mutex
	last    SyncResult
}

// SyncResult is the outcome of one pass of Syncer.Sync, shown on the Add-ons page.
type SyncResult struct {
	At       time.Time         `json:"at"`
	Commit   string            `json:"commit,omitempty"`
	Applied  []string          `json:"applied"`
	Removed  []string          `json:"removed"`
	Invalid  map[string]string `json:"invalid,omitempty"` // file → error
	Error    string            `json:"error,omitempty"`
	Branch   string            `json:"branch,omitempty"`
	Repo     string            `json:"repo"`
	Duration string            `json:"duration"`
}

func (s *Syncer) init() { s.once.Do(func() { s.trigger = make(chan struct{}, 1) }) }

// Trigger asks the loop to sync soon (e.g. after a push to the repo).
func (s *Syncer) Trigger() {
	s.init()
	select {
	case s.trigger <- struct{}{}:
	default:
	}
}

// Last returns the most recent sync's outcome.
func (s *Syncer) Last() SyncResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

// Loop syncs every few minutes and whenever triggered, until ctx ends.
func (s *Syncer) Loop(ctx context.Context) {
	s.init()
	t := time.NewTicker(3 * time.Minute)
	defer t.Stop()
	for {
		res := s.Sync(ctx)
		if res.Error != "" {
			s.Log.Warn("addons: sync failed", "repo", s.Repo, "err", res.Error)
		} else if len(res.Applied)+len(res.Removed)+len(res.Invalid) > 0 {
			s.Log.Info("addons: synced from git", "commit", res.Commit, "applied", res.Applied, "removed", res.Removed, "invalid", res.Invalid)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.trigger:
		}
	}
}

// Sync makes the Addon objects match the definitions at the head of the gitops repository: it
// creates and updates them, and deletes those whose file is gone (their software keeps running
// unless marked for uninstall).
func (s *Syncer) Sync(ctx context.Context) SyncResult {
	start := time.Now()
	res := SyncResult{At: start, Repo: s.Repo, Applied: []string{}, Removed: []string{}}
	defer func() {
		res.Duration = time.Since(start).Round(time.Millisecond).String()
		s.mu.Lock()
		s.last = res
		s.mu.Unlock()
	}()
	defs, sha, invalid, err := s.read(ctx)
	res.Commit, res.Invalid = sha, invalid
	if err != nil {
		res.Error = err.Error()
		return res
	}
	var existing v1alpha1.AddonList
	if err := s.Kube.List(ctx, &existing, client.MatchingLabels{LabelGitops: "true"}); err != nil {
		res.Error = err.Error()
		return res
	}
	for file, d := range defs {
		changed, err := s.apply(ctx, file, d, sha)
		if err != nil {
			if res.Invalid == nil {
				res.Invalid = map[string]string{}
			}
			res.Invalid[file] = err.Error()
			continue
		}
		if changed {
			res.Applied = append(res.Applied, d.Name)
		}
	}
	names := map[string]bool{}
	for _, d := range defs {
		names[d.Name] = true
	}
	// A definition that is only invalid must not remove its add-on.
	for file := range invalid {
		names[strings.TrimSuffix(path.Base(file), path.Ext(file))] = true
	}
	for i := range existing.Items {
		a := &existing.Items[i]
		if names[a.Name] {
			continue
		}
		// The file was deleted: remove the Addon. Its objects keep running
		// unless the uninstall annotation was set (by the UI's uninstall).
		if err := s.Kube.Delete(ctx, a); client.IgnoreNotFound(err) != nil {
			res.Error = err.Error()
			return res
		}
		res.Removed = append(res.Removed, a.Name)
	}
	return res
}

func (s *Syncer) read(ctx context.Context) (map[string]*Definition, string, map[string]string, error) {
	dir, sha, err := GitHubFetcher{GitHub: s.GitHub}.Fetch(ctx, s.Repo, "", "")
	if err != nil {
		return nil, "", nil, err
	}
	defs := map[string]*Definition{}
	invalid := map[string]string{}
	entries, err := fs.ReadDir(dir, Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return defs, sha, nil, nil
	}
	if err != nil {
		return nil, sha, nil, err
	}
	seen := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || (path.Ext(e.Name()) != ".yaml" && path.Ext(e.Name()) != ".yml") {
			continue
		}
		file := Dir + "/" + e.Name()
		data, err := fs.ReadFile(dir, file)
		if err != nil {
			return nil, sha, nil, err
		}
		d, err := ParseDefinition(data)
		if err != nil {
			invalid[file] = err.Error()
			continue
		}
		if other, dup := seen[d.Name]; dup {
			invalid[file] = fmt.Sprintf("name %q is also used by %s", d.Name, other)
			continue
		}
		seen[d.Name] = file
		defs[file] = d
	}
	if len(invalid) == 0 {
		invalid = nil
	}
	return defs, sha, invalid, nil
}

// apply creates or updates one Addon; it reports whether anything changed.
func (s *Syncer) apply(ctx context.Context, file string, d *Definition, sha string) (bool, error) {
	spec, err := d.Spec(s.Repo, sha)
	if err != nil {
		return false, err
	}
	var a v1alpha1.Addon
	err = s.Kube.Get(ctx, client.ObjectKey{Name: d.Name}, &a)
	if apierrors.IsNotFound(err) {
		a = v1alpha1.Addon{ObjectMeta: metav1.ObjectMeta{
			Name:        d.Name,
			Labels:      map[string]string{LabelGitops: "true"},
			Annotations: map[string]string{AnnotationSource: file + "@" + sha},
		}, Spec: spec}
		return true, s.Kube.Create(ctx, &a)
	}
	if err != nil {
		return false, err
	}
	if a.Labels[LabelGitops] != "true" {
		return false, fmt.Errorf("an add-on named %q exists that is not managed from git", d.Name)
	}
	// SyncRequest is set from the UI (manual sync), not from git.
	spec.SyncRequest = a.Spec.SyncRequest
	if equalSpec(a.Spec, spec) {
		return false, nil
	}
	a.Spec = spec
	if a.Annotations == nil {
		a.Annotations = map[string]string{}
	}
	a.Annotations[AnnotationSource] = file + "@" + sha
	return true, s.Kube.Update(ctx, &a)
}

func equalSpec(a, b v1alpha1.AddonSpec) bool {
	x, _ := yaml.Marshal(a)
	y, _ := yaml.Marshal(b)
	return string(x) == string(y)
}
