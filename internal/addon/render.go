// Package addon renders add-ons (Helm charts or kustomize folders in git)
// into Kubernetes objects. It is what `helm template` and `kustomize build`
// do, run inside rendimiento, so the controller can preview and apply them.
package addon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/repo"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/discovery"
	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/kyaml/filesys"
	"sigs.k8s.io/yaml"

	"github.com/p0dxD/rendimiento.ai/api/v1alpha1"
)

// GitFetcher reads a folder of a GitHub repository at a commit.
type GitFetcher interface {
	// Fetch resolves ref (a branch or commit; empty = default branch) to a
	// commit and returns the folder at path in it.
	Fetch(ctx context.Context, repo, path, ref string) (fs.FS, string, error)
}

// Result is a rendered add-on.
type Result struct {
	Objects []*unstructured.Unstructured
	// Revision is the chart version or git commit that was rendered.
	Revision string
	// SkippedHooks are Helm hooks ("kind/name (hook)"), which are not run.
	SkippedHooks []string
}

type Renderer struct {
	Git       GitFetcher
	Discovery discovery.DiscoveryInterface
	HTTP      *http.Client

	mu     sync.Mutex
	charts map[string]*chart.Chart // repo|chart|version → chart
}

func (r *Renderer) Render(ctx context.Context, a *v1alpha1.Addon) (*Result, error) {
	src := a.Spec.Source
	switch {
	case src.Helm != nil && src.Git == nil:
		return r.renderHelm(ctx, a)
	case src.Git != nil && src.Helm == nil:
		return r.renderGit(ctx, a)
	}
	return nil, errors.New("source needs exactly one of helm or git")
}

// ---- Helm ----

func (r *Renderer) renderHelm(ctx context.Context, a *v1alpha1.Addon) (*Result, error) {
	h := a.Spec.Source.Helm
	ch, err := r.chart(ctx, h.Repo, h.Chart, h.Version)
	if err != nil {
		return nil, err
	}
	vals, err := chartutil.ReadValues([]byte(a.Spec.Values))
	if err != nil {
		return nil, fmt.Errorf("values: %w", err)
	}
	inst := action.NewInstall(&action.Configuration{})
	inst.DryRun, inst.DryRunOption, inst.ClientOnly, inst.Replace = true, "client", true, true
	inst.IncludeCRDs = true
	inst.ReleaseName = a.Spec.ReleaseName
	if inst.ReleaseName == "" {
		inst.ReleaseName = a.Name
	}
	inst.Namespace = a.Spec.Namespace
	if r.Discovery != nil {
		// Render for this cluster, as ArgoCD and `helm install` would, rather
		// than for Helm's built-in default Kubernetes version.
		if v, err := r.Discovery.ServerVersion(); err == nil {
			inst.KubeVersion = &chartutil.KubeVersion{Version: v.GitVersion, Major: v.Major, Minor: strings.TrimSuffix(v.Minor, "+")}
		}
		if vs, err := action.GetVersionSet(r.Discovery); err == nil {
			inst.APIVersions = vs
		}
	}
	rel, err := inst.RunWithContext(ctx, ch, vals)
	if err != nil {
		return nil, fmt.Errorf("render %s %s: %w", h.Chart, h.Version, err)
	}
	objs, err := Decode([]byte(rel.Manifest))
	if err != nil {
		return nil, err
	}
	res := &Result{Objects: objs, Revision: h.Chart + "-" + ch.Metadata.Version}
	for _, hook := range rel.Hooks {
		res.SkippedHooks = append(res.SkippedHooks, fmt.Sprintf("%s (%s)", hook.Path, joinEvents(hook.Events)))
	}
	sort.Strings(res.SkippedHooks)
	return res, nil
}

func joinEvents[T fmt.Stringer](evs []T) string {
	var s []string
	for _, e := range evs {
		s = append(s, e.String())
	}
	return strings.Join(s, ",")
}

// chart downloads a chart from a classic (index.yaml) Helm repository.
func (r *Renderer) chart(ctx context.Context, repoURL, name, version string) (*chart.Chart, error) {
	if strings.HasPrefix(repoURL, "oci://") {
		return nil, errors.New("OCI chart registries are not supported yet; use the chart's https repository")
	}
	key := repoURL + "|" + name + "|" + version
	r.mu.Lock()
	if c := r.charts[key]; c != nil {
		r.mu.Unlock()
		return c, nil
	}
	r.mu.Unlock()

	base, err := url.Parse(strings.TrimSuffix(repoURL, "/") + "/")
	if err != nil {
		return nil, fmt.Errorf("chart repository %q: %w", repoURL, err)
	}
	data, err := r.get(ctx, base.ResolveReference(&url.URL{Path: "index.yaml"}).String())
	if err != nil {
		return nil, fmt.Errorf("chart repository index: %w", err)
	}
	var idx repo.IndexFile
	if err := yaml.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("chart repository index: %w", err)
	}
	var found *repo.ChartVersion
	for _, v := range idx.Entries[name] {
		if v.Metadata != nil && strings.TrimPrefix(v.Version, "v") == strings.TrimPrefix(version, "v") {
			found = v
			break
		}
	}
	if found == nil || len(found.URLs) == 0 {
		return nil, fmt.Errorf("chart %s version %s not found in %s", name, version, repoURL)
	}
	u, err := url.Parse(found.URLs[0])
	if err != nil {
		return nil, err
	}
	archive, err := r.get(ctx, base.ResolveReference(u).String())
	if err != nil {
		return nil, fmt.Errorf("download chart: %w", err)
	}
	c, err := loader.LoadArchive(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("load chart: %w", err)
	}
	r.mu.Lock()
	if r.charts == nil {
		r.charts = map[string]*chart.Chart{}
	}
	r.charts[key] = c
	r.mu.Unlock()
	return c, nil
}

func (r *Renderer) get(ctx context.Context, u string) ([]byte, error) {
	hc := r.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 2 * time.Minute}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

// ---- git (kustomize or plain manifests) ----

func (r *Renderer) renderGit(ctx context.Context, a *v1alpha1.Addon) (*Result, error) {
	g := a.Spec.Source.Git
	if r.Git == nil {
		return nil, errors.New("git sources are not configured")
	}
	dir, sha, err := r.Git.Fetch(ctx, g.Repo, g.Path, g.Revision)
	if err != nil {
		return nil, fmt.Errorf("fetch %s/%s: %w", g.Repo, g.Path, err)
	}
	objs, err := Build(dir)
	if err != nil {
		return nil, fmt.Errorf("%s/%s: %w", g.Repo, g.Path, err)
	}
	return &Result{Objects: objs, Revision: sha}, nil
}

// Build renders a folder: kustomize if it has a kustomization file,
// otherwise every YAML file in it.
func Build(dir fs.FS) ([]*unstructured.Unstructured, error) {
	mem := filesys.MakeFsInMemory()
	hasKustomization := false
	err := fs.WalkDir(dir, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(dir, p)
		if err != nil {
			return err
		}
		switch path.Base(p) {
		case "kustomization.yaml", "kustomization.yml", "Kustomization":
			if path.Dir(p) == "." {
				hasKustomization = true
			}
		}
		return mem.WriteFile(path.Join("/src", p), data)
	})
	if err != nil {
		return nil, err
	}
	if !hasKustomization {
		var out []*unstructured.Unstructured
		files, _ := fs.Glob(dir, "*.y*ml")
		sort.Strings(files)
		for _, f := range files {
			data, err := fs.ReadFile(dir, f)
			if err != nil {
				return nil, err
			}
			objs, err := Decode(data)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", f, err)
			}
			out = append(out, objs...)
		}
		return out, nil
	}
	k := krusty.MakeKustomizer(krusty.MakeDefaultOptions())
	rm, err := k.Run(mem, "/src")
	if err != nil {
		return nil, fmt.Errorf("kustomize: %w", err)
	}
	data, err := rm.AsYaml()
	if err != nil {
		return nil, err
	}
	return Decode(data)
}

// Decode splits multi-document YAML into objects, expanding Lists and
// skipping empty documents.
func Decode(data []byte) ([]*unstructured.Unstructured, error) {
	dec := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
	var out []*unstructured.Unstructured
	for {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			if errors.Is(err, io.EOF) {
				return out, nil
			}
			return nil, err
		}
		if len(m) == 0 {
			continue
		}
		u := &unstructured.Unstructured{Object: m}
		if u.IsList() {
			list, err := u.ToList()
			if err != nil {
				return nil, err
			}
			for i := range list.Items {
				out = append(out, &list.Items[i])
			}
			continue
		}
		if u.GetKind() == "" || u.GetName() == "" {
			return nil, fmt.Errorf("a document has no kind or name")
		}
		out = append(out, u)
	}
}
