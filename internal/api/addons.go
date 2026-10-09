package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/renovate"
	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// addonView is an add-on as the UI shows it.
type addonView struct {
	Name        string            `json:"name"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Enabled     bool              `json:"enabled"`
	Settings    renovate.Settings `json:"settings"`
	Running     bool              `json:"running"`
	NextRun     *time.Time        `json:"nextRun,omitempty"`
	Repos       []string          `json:"repos"`
	Apps        []addonApp        `json:"apps"`
	LastRun     *store.AddonRun   `json:"lastRun,omitempty"`
	// Problems would stop a run (e.g. missing GitHub App permissions);
	// PermissionsURL is where the app's permissions are changed.
	Problems       []string           `json:"problems"`
	PermissionsURL string             `json:"permissionsUrl"`
	Defaults       *renovate.Settings `json:"defaults,omitempty"`
}

type addonApp struct {
	Name    string `json:"name"`
	Repo    string `json:"repo"`
	Enabled bool   `json:"enabled"`
}

// renovateView is the Renovate add-on as its page shows it: on or off, its
// settings (or the defaults), its last run and when the next one is.
func (s *Server) renovateView(r *http.Request) (*addonView, error) {
	ctx := r.Context()
	enabled, set, row, err := renovate.Load(ctx, s.Store)
	if err != nil {
		return nil, err
	}
	def := renovate.DefaultSettings()
	v := &addonView{
		Name:  renovate.Name,
		Title: "Renovate",
		Description: "Keeps dependencies up to date: opens a pull request for each update, which rendimiento builds and checks; " +
			"patch and minor updates merge themselves once the checks pass.",
		Enabled: enabled, Settings: set, Running: s.Renovate != nil && s.Renovate.Running(), Defaults: &def,
		Repos: []string{}, Apps: []addonApp{}, Problems: []string{},
		PermissionsURL: "https://github.com/settings/apps/" + s.AppName + "/permissions",
	}
	if s.Renovate != nil {
		v.Problems = append(v.Problems, s.Renovate.Problems(ctx)...)
	}
	if enabled && row != nil {
		from := time.Now()
		if row.LastScheduled != nil {
			from = *row.LastScheduled
		}
		if next := set.Next(from); !next.IsZero() {
			v.NextRun = &next
		}
	}
	if s.Renovate != nil {
		if v.Repos, err = s.Renovate.Repos(ctx, set); err != nil {
			return nil, err
		}
	}
	apps, err := s.Store.ListApps(ctx)
	if err != nil {
		return nil, err
	}
	on, err := s.Store.AppsWithAddon(ctx, renovate.Name)
	if err != nil {
		return nil, err
	}
	isOn := map[int64]bool{}
	for _, a := range on {
		isOn[a.ID] = true
	}
	for _, a := range apps {
		v.Apps = append(v.Apps, addonApp{Name: a.Name, Repo: a.Repo, Enabled: isOn[a.ID]})
	}
	if runs, err := s.Store.ListAddonRuns(ctx, renovate.Name, 1); err == nil && len(runs) > 0 {
		v.LastRun = runs[0]
	}
	return v, nil
}

// listAddons is the built-in add-ons (Renovate).
func (s *Server) listAddons(w http.ResponseWriter, r *http.Request, _ string) {
	v, err := s.renovateView(r)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, []*addonView{v})
}

// getAddon is one built-in add-on's page.
func (s *Server) getAddon(w http.ResponseWriter, r *http.Request, _ string) {
	if r.PathValue("addon") != renovate.Name {
		httpError(w, http.StatusNotFound, "no such add-on")
		return
	}
	v, err := s.renovateView(r)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, v)
}

// putAddon switches a built-in add-on on or off and saves its settings.
func (s *Server) putAddon(w http.ResponseWriter, r *http.Request, login string) {
	if r.PathValue("addon") != renovate.Name {
		httpError(w, http.StatusNotFound, "no such add-on")
		return
	}
	var req struct {
		Enabled  bool              `json:"enabled"`
		Settings renovate.Settings `json:"settings"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	req.Settings.Image = strings.TrimSpace(req.Settings.Image)
	req.Settings.Schedule = strings.TrimSpace(req.Settings.Schedule)
	extra := []string{}
	for _, repo := range req.Settings.ExtraRepos {
		if repo = strings.TrimSpace(repo); repo != "" {
			extra = append(extra, repo)
		}
	}
	req.Settings.ExtraRepos = extra
	if err := req.Settings.Validate(); err != nil {
		httpError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	raw, _ := json.Marshal(req.Settings)
	if err := s.Store.PutAddon(r.Context(), renovate.Name, req.Enabled, raw); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Log.Info("add-on updated", "addon", renovate.Name, "enabled", req.Enabled, "by", login)
	s.getAddon(w, r, login)
}

// startAddonRun runs the add-on now ("Run now"), unless a run is going.
func (s *Server) startAddonRun(w http.ResponseWriter, r *http.Request, login string) {
	if r.PathValue("addon") != renovate.Name || s.Renovate == nil {
		httpError(w, http.StatusNotFound, "no such add-on")
		return
	}
	run, err := s.Renovate.Start(r.Context(), "manual")
	switch {
	case errors.Is(err, renovate.ErrBusy):
		httpError(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		httpError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	s.Log.Info("add-on run started", "addon", renovate.Name, "run", run.ID, "by", login)
	writeJSONStatus(w, http.StatusCreated, run)
}

// listAddonRuns is the add-on's last 30 runs.
func (s *Server) listAddonRuns(w http.ResponseWriter, r *http.Request, _ string) {
	runs, err := s.Store.ListAddonRuns(r.Context(), r.PathValue("addon"), 30)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, runs)
}

// getAddonRun is one run of an add-on, with what it did per repository.
func (s *Server) getAddonRun(w http.ResponseWriter, r *http.Request, _ string) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpError(w, http.StatusBadRequest, "bad run id")
		return
	}
	run, err := s.Store.GetAddonRun(r.Context(), r.PathValue("addon"), id)
	if errors.Is(err, store.ErrNotFound) {
		httpError(w, http.StatusNotFound, "no such run")
		return
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, run)
}

// appAddons is the app page's view: its switch and what the last run did.
func (s *Server) appAddons(w http.ResponseWriter, r *http.Request, _ string) {
	a := s.app(w, r)
	if a == nil {
		return
	}
	ctx := r.Context()
	switches, err := s.Store.AppAddons(ctx, a.ID)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	addonOn, _, _, err := renovate.Load(ctx, s.Store)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := map[string]any{
		"enabled":      switches[renovate.Name],
		"addonEnabled": addonOn,
		"prsUrl":       "https://github.com/" + a.Repo + "/pulls?q=" + url.QueryEscape("is:pr is:open author:app/"+s.AppName),
	}
	if runs, err := s.Store.ListAddonRuns(ctx, renovate.Name, 20); err == nil {
		for _, run := range runs {
			if res, ok := run.Results[a.Repo]; ok {
				out["lastRun"] = map[string]any{"id": run.ID, "status": run.Status, "startedAt": run.StartedAt, "result": res}
				break
			}
		}
	}
	writeJSON(w, map[string]any{renovate.Name: out})
}

// putAppAddon switches Renovate on or off for one app.
func (s *Server) putAppAddon(w http.ResponseWriter, r *http.Request, login string) {
	a := s.app(w, r)
	if a == nil {
		return
	}
	if r.PathValue("addon") != renovate.Name {
		httpError(w, http.StatusNotFound, "no such add-on")
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.Store.SetAppAddon(r.Context(), a.ID, renovate.Name, req.Enabled); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Log.Info("app add-on switched", "app", a.Name, "addon", renovate.Name, "enabled", req.Enabled, "by", login)
	s.appAddons(w, r, login)
}
