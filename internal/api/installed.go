package api

import (
	"net/http"
	"sort"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/p0dxD/rendimiento.ai/api/v1alpha1"
	"github.com/p0dxD/rendimiento.ai/internal/addon"
	"github.com/p0dxD/rendimiento.ai/internal/controller"
	gh "github.com/p0dxD/rendimiento.ai/internal/github"
)

// installedView summarizes an installed add-on.
type installedView struct {
	Name        string               `json:"name"`
	Title       string               `json:"title"`
	Category    string               `json:"category"`
	Description string               `json:"description"`
	Namespace   string               `json:"namespace"`
	Source      v1alpha1.AddonSource `json:"source"`
	ManualSync  bool                 `json:"manualSync"`
	Suspend     bool                 `json:"suspend"`
	Adopt       bool                 `json:"adopt"`
	FromGit     bool                 `json:"fromGit"`
	SourceFile  string               `json:"sourceFile,omitempty"`
	Status      v1alpha1.AddonStatus `json:"status"`
	Preview     *v1alpha1.Preview    `json:"preview,omitempty"`
	Spec        *v1alpha1.AddonSpec  `json:"spec,omitempty"`
	Definition  string               `json:"definition,omitempty"`
}

// view is an installed add-on as the UI shows it (with its full definition
// when full).
func view(a *v1alpha1.Addon, full bool) installedView {
	v := installedView{
		Name: a.Name, Title: a.Spec.Title, Category: a.Spec.Category, Description: a.Spec.Description,
		Namespace: a.Spec.Namespace, Source: a.Spec.Source, ManualSync: a.Spec.ManualSync, Suspend: a.Spec.Suspend,
		Adopt: a.Spec.Adopt, FromGit: a.Labels[addon.LabelGitops] == "true", SourceFile: a.Annotations[addon.AnnotationSource],
		Status: a.Status,
	}
	if v.Title == "" {
		v.Title = a.Name
	}
	if !full {
		// Lists carry counts only; diffs and the inventory come with the detail.
		if p := a.Status.Preview; p != nil {
			v.Status.Preview = &v1alpha1.Preview{Create: p.Create, Update: p.Update, Unchanged: p.Unchanged, Prune: p.Prune}
		}
		v.Status.Objects = nil
		return v
	}
	spec := a.Spec
	v.Spec = &spec
	return v
}

// addonCatalog is the add-ons that can be installed in one click.
func (s *Server) addonCatalog(w http.ResponseWriter, _ *http.Request, _ string) {
	writeJSON(w, addon.Catalog)
}

// listInstalled is every installed add-on (Addon objects in the cluster).
func (s *Server) listInstalled(w http.ResponseWriter, r *http.Request, _ string) {
	var list v1alpha1.AddonList
	if err := s.Kube.List(r.Context(), &list); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := []installedView{}
	for i := range list.Items {
		out = append(out, view(&list.Items[i], false))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	resp := map[string]any{"addons": out}
	if s.Addons != nil {
		resp["repo"] = s.Addons.Repo
		resp["sync"] = s.Addons.Last()
	}
	writeJSON(w, resp)
}

// getInstalled is one installed add-on with its definition.
func (s *Server) getInstalled(w http.ResponseWriter, r *http.Request, _ string) {
	var a v1alpha1.Addon
	if err := s.Kube.Get(r.Context(), client.ObjectKey{Name: r.PathValue("name")}, &a); err != nil {
		if apierrors.IsNotFound(err) {
			httpError(w, http.StatusNotFound, "no such add-on")
			return
		}
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	v := view(&a, true)
	if v.FromGit && s.Addons != nil {
		if c, branch, err := s.gitopsClient(r); err == nil {
			if raw, err := c.FileAt(r.Context(), s.Addons.Repo, addon.Dir+"/"+a.Name+".yaml", branch); err == nil {
				v.Definition = string(raw)
			}
		}
	}
	writeJSON(w, v)
}

// owner is the account part of owner/repo.
func owner(repo string) string {
	o, _, _ := strings.Cut(repo, "/")
	return o
}

// putInstalled installs or changes an add-on by committing its definition
// to the gitops repository; the sync then applies it.
func (s *Server) putInstalled(w http.ResponseWriter, r *http.Request, login string) {
	if s.Addons == nil {
		httpError(w, http.StatusServiceUnavailable, "add-ons are not configured")
		return
	}
	var req struct {
		Definition *addon.Definition `json:"definition"`
		// ValuesYAML overrides the definition's values (deep merge), so the
		// install form can combine its fields with free-form values.
		ValuesYAML string `json:"valuesYaml"`
		YAML       string `json:"yaml"` // a whole definition, as edited in the UI
	}
	if !readJSON(w, r, &req) {
		return
	}
	d := req.Definition
	if req.YAML != "" {
		parsed, err := addon.ParseDefinition([]byte(req.YAML))
		if err != nil {
			httpError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		d = parsed
	}
	if d == nil {
		httpError(w, http.StatusBadRequest, "send a definition or its YAML")
		return
	}
	if strings.TrimSpace(req.ValuesYAML) != "" {
		var override map[string]any
		if err := yaml.Unmarshal([]byte(req.ValuesYAML), &override); err != nil {
			httpError(w, http.StatusUnprocessableEntity, "values: "+err.Error())
			return
		}
		if d.Values == nil {
			d.Values = map[string]any{}
		}
		deepMerge(d.Values, override)
	}
	if d.Name != r.PathValue("name") {
		httpError(w, http.StatusUnprocessableEntity, "the definition's name must match the URL")
		return
	}
	if err := d.Validate(); err != nil {
		httpError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	// Refuse to take over an Addon that git does not manage.
	var existing v1alpha1.Addon
	err := s.Kube.Get(r.Context(), client.ObjectKey{Name: d.Name}, &existing)
	if err == nil && existing.Labels[addon.LabelGitops] != "true" {
		httpError(w, http.StatusConflict, "an add-on with this name exists that is not managed from git")
		return
	}
	verb := "update"
	if apierrors.IsNotFound(err) {
		verb = "install"
	}
	content, err := d.Marshal()
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	commit, err := s.commitDefinition(r, d.Name, string(content), "addons: "+verb+" "+d.Name+" (by "+login+" via rendimiento)")
	if err != nil {
		httpError(w, http.StatusBadGateway, "commit to "+s.Addons.Repo+": "+err.Error())
		return
	}
	s.Addons.Trigger()
	s.Log.Info("add-on committed", "addon", d.Name, "verb", verb, "commit", commit, "by", login)
	writeJSON(w, map[string]string{"commit": commit, "repo": s.Addons.Repo})
}

// commitDefinition writes an add-on's definition to the gitops repository,
// where add-ons are kept (ADDONS_REPO), and returns the commit.
func (s *Server) commitDefinition(r *http.Request, name, content, message string) (string, error) {
	c, branch, err := s.gitopsClient(r)
	if err != nil {
		return "", err
	}
	return c.PutFile(r.Context(), s.Addons.Repo, branch, addon.Dir+"/"+name+".yaml", content, message)
}

// gitopsClient is a GitHub client for the gitops repository and its default
// branch.
func (s *Server) gitopsClient(r *http.Request) (*gh.Client, string, error) {
	c, _, err := s.GitHub.ForOwner(r.Context(), owner(s.Addons.Repo))
	if err != nil {
		return nil, "", err
	}
	repo, err := c.Repo(r.Context(), s.Addons.Repo)
	if err != nil {
		return nil, "", err
	}
	return c, repo.DefaultBranch, nil
}

// syncInstalled asks a manual-sync add-on to apply its pending changes.
func (s *Server) syncInstalled(w http.ResponseWriter, r *http.Request, login string) {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var a v1alpha1.Addon
		if err := s.Kube.Get(r.Context(), client.ObjectKey{Name: r.PathValue("name")}, &a); err != nil {
			return err
		}
		a.Spec.SyncRequest = a.Status.AppliedSyncRequest + 1
		return s.Kube.Update(r.Context(), &a)
	})
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Log.Info("add-on sync requested", "addon", r.PathValue("name"), "by", login)
	w.WriteHeader(http.StatusAccepted)
}

// deleteInstalled removes the definition from git. With ?uninstall=true
// the add-on's objects are deleted too (never CRDs, namespaces or
// volumes); otherwise they keep running, no longer managed.
func (s *Server) deleteInstalled(w http.ResponseWriter, r *http.Request, login string) {
	if s.Addons == nil {
		httpError(w, http.StatusServiceUnavailable, "add-ons are not configured")
		return
	}
	name := r.PathValue("name")
	uninstall := r.URL.Query().Get("uninstall") == "true"
	if uninstall {
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			var a v1alpha1.Addon
			if err := s.Kube.Get(r.Context(), client.ObjectKey{Name: name}, &a); err != nil {
				return err
			}
			if a.Annotations == nil {
				a.Annotations = map[string]string{}
			}
			a.Annotations[controller.AnnotationUninstall] = "true"
			return s.Kube.Update(r.Context(), &a)
		})
		if err != nil && !apierrors.IsNotFound(err) {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	c, branch, err := s.gitopsClient(r)
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	verb := "remove"
	if uninstall {
		verb = "uninstall"
	}
	commit, err := c.DeleteFile(r.Context(), s.Addons.Repo, branch, addon.Dir+"/"+name+".yaml", "addons: "+verb+" "+name+" (by "+login+" via rendimiento)")
	if err != nil && !gh.IsNotFound(err) {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.Addons.Trigger()
	s.Log.Info("add-on removed from git", "addon", name, "uninstall", uninstall, "commit", commit, "by", login)
	writeJSON(w, map[string]string{"commit": commit})
}

// deepMerge copies src into dst, merging nested maps.
func deepMerge(dst, src map[string]any) {
	for k, v := range src {
		if sm, ok := v.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				deepMerge(dm, sm)
				continue
			}
		}
		dst[k] = v
	}
}

// patchInstalled flips switches in an add-on's definition (suspend, allow
// adoption changes) and commits it.
func (s *Server) patchInstalled(w http.ResponseWriter, r *http.Request, login string) {
	if s.Addons == nil {
		httpError(w, http.StatusServiceUnavailable, "add-ons are not configured")
		return
	}
	var req struct {
		Suspend           *bool `json:"suspend"`
		AllowAdoptChanges *bool `json:"allowAdoptChanges"`
		ManualSync        *bool `json:"manualSync"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	name := r.PathValue("name")
	c, branch, err := s.gitopsClient(r)
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	raw, err := c.FileAt(r.Context(), s.Addons.Repo, addon.Dir+"/"+name+".yaml", branch)
	if err != nil {
		httpError(w, http.StatusNotFound, "no definition for "+name+" in "+s.Addons.Repo)
		return
	}
	d, err := addon.ParseDefinition(raw)
	if err != nil {
		httpError(w, http.StatusUnprocessableEntity, "the definition in git is invalid: "+err.Error())
		return
	}
	var what []string
	if req.Suspend != nil {
		d.Suspend = *req.Suspend
		what = append(what, map[bool]string{true: "suspend", false: "resume"}[*req.Suspend])
	}
	if req.AllowAdoptChanges != nil {
		d.AllowAdoptChanges = *req.AllowAdoptChanges
		what = append(what, "allow adoption changes")
	}
	if req.ManualSync != nil {
		d.ManualSync = *req.ManualSync
		what = append(what, map[bool]string{true: "manual sync", false: "automatic sync"}[*req.ManualSync])
	}
	if len(what) == 0 {
		httpError(w, http.StatusBadRequest, "nothing to change")
		return
	}
	content, err := d.Marshal()
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	commit, err := c.PutFile(r.Context(), s.Addons.Repo, branch, addon.Dir+"/"+name+".yaml", string(content),
		"addons: "+name+": "+strings.Join(what, ", ")+" (by "+login+" via rendimiento)")
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.Addons.Trigger()
	writeJSON(w, map[string]string{"commit": commit})
}
