// Package api serves the REST + SSE API, GitHub webhooks, login, one-click
// GitHub App setup, and the embedded web UI.
package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/p0dxD/rendimiento.ai/api/v1alpha1"
	"github.com/p0dxD/rendimiento.ai/internal/addon"
	"github.com/p0dxD/rendimiento.ai/internal/catalog"
	"github.com/p0dxD/rendimiento.ai/internal/dns"
	"github.com/p0dxD/rendimiento.ai/internal/environment"
	gh "github.com/p0dxD/rendimiento.ai/internal/github"
	"github.com/p0dxD/rendimiento.ai/internal/logarchive"
	"github.com/p0dxD/rendimiento.ai/internal/notify"
	"github.com/p0dxD/rendimiento.ai/internal/platform"
	"github.com/p0dxD/rendimiento.ai/internal/render"
	"github.com/p0dxD/rendimiento.ai/internal/renovate"
	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// CredentialStore persists the GitHub App credentials (a Kubernetes Secret in production).
type CredentialStore interface {
	Load(ctx context.Context) (*gh.Credentials, error) // nil, nil when absent
	Save(ctx context.Context, c *gh.Credentials) error
}

// Server holds everything the HTTP API needs; Handler returns its routes.
type Server struct {
	Platform    *platform.Platform
	Store       *store.Store
	Kube        client.Client
	GitHub      *gh.Holder
	Credentials CredentialStore
	DNS         dns.Provider
	Log         *slog.Logger
	BaseURL     string // https://rendimiento.joserod.space
	// Notify sends notification emails (the test email; nil when off).
	Notify *notify.Notifier
	// PublicStats serves GET /api/public/stats without login, for a public
	// page; PublicOrigins may fetch it from browsers; PublicTimeZone counts
	// its days (an IANA name, default UTC).
	PublicStats    bool
	PublicOrigins  []string
	PublicTimeZone string
	// Logs reads step logs archived to object storage (nil when off).
	Logs         *logarchive.Archive
	public       publicCache
	AllowedUsers []string // GitHub logins allowed to sign in
	SetupToken   string   // guards the one-time GitHub App setup
	AppName      string   // name for the GitHub App, e.g. "rendimiento-joserod"
	GitHubAPI    string   // overridden in tests
	UI           fs.FS    // built web UI; nil serves a placeholder
	Environment  *environment.Checker
	Catalog      *catalog.Builder
	Renovate     *renovate.Runner
	Addons       *addon.Syncer
}

const sessionCookie = "rendimiento_session"

// Handler returns the HTTP handler: public routes (health, webhooks, login, setup),
// session-protected /api routes and the embedded UI.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok") })

	// Public endpoints.
	mux.HandleFunc("POST /api/webhooks/github", s.webhook)
	mux.HandleFunc("GET /api/public/stats", s.publicStats)
	mux.HandleFunc("GET /api/auth/login", s.login)
	mux.HandleFunc("GET /api/auth/callback", s.callback)
	mux.HandleFunc("POST /api/auth/logout", s.logout)
	mux.HandleFunc("GET /api/setup/status", s.setupStatus)
	mux.HandleFunc("GET /api/setup/github", s.setupStart)
	mux.HandleFunc("GET /api/setup/github/callback", s.setupCallback)

	// Everything else requires a session.
	auth := func(pattern string, h func(http.ResponseWriter, *http.Request, string)) {
		mux.Handle(pattern, s.requireSession(h))
	}
	auth("GET /api/me", func(w http.ResponseWriter, _ *http.Request, login string) {
		writeJSON(w, map[string]string{"login": login})
	})
	auth("GET /api/installations", s.installations)
	auth("GET /api/installations/{id}/repos", s.repos)
	auth("GET /api/zones", s.zones)
	auth("GET /api/environment", s.environment)
	auth("GET /api/services", s.services)
	auth("GET /api/addon-catalog", s.addonCatalog)
	auth("GET /api/installed", s.listInstalled)
	auth("GET /api/installed/{name}", s.getInstalled)
	auth("PUT /api/installed/{name}", s.putInstalled)
	auth("POST /api/installed/{name}/sync", s.syncInstalled)
	auth("PATCH /api/installed/{name}", s.patchInstalled)
	auth("DELETE /api/installed/{name}", s.deleteInstalled)
	auth("GET /api/addons", s.listAddons)
	auth("GET /api/addons/{addon}", s.getAddon)
	auth("PUT /api/addons/{addon}", s.putAddon)
	auth("GET /api/addons/{addon}/runs", s.listAddonRuns)
	auth("POST /api/addons/{addon}/runs", s.startAddonRun)
	auth("GET /api/addons/{addon}/runs/{id}", s.getAddonRun)
	auth("GET /api/apps/{app}/addons", s.appAddons)
	auth("PUT /api/apps/{app}/addons/{addon}", s.putAppAddon)
	auth("POST /api/dns/sync", s.dnsSync)
	auth("POST /api/notifications/test", s.testNotification)
	auth("POST /api/propose", s.propose)
	auth("GET /api/namespaces/{name}/migration", s.migration)
	auth("GET /api/apps", s.listApps)
	auth("POST /api/apps", s.createApp)
	auth("GET /api/apps/{app}", s.getApp)
	auth("DELETE /api/apps/{app}", s.deleteApp)
	auth("GET /api/apps/{app}/impact", s.deleteImpact)
	auth("POST /api/apps/{app}/disconnect", s.disconnectApp)
	auth("GET /api/apps/{app}/runs", s.listRuns)
	auth("POST /api/apps/{app}/runs", s.triggerRun)
	auth("GET /api/apps/{app}/releases", s.listReleases)
	auth("GET /api/apps/{app}/reliability", s.reliability)
	auth("GET /api/apps/{app}/releases/{number}/tasks/{task}/log", s.releaseTaskLog)
	auth("POST /api/apps/{app}/rollback", s.rollback)
	auth("GET /api/apps/{app}/resources", s.resources)
	auth("PUT /api/apps/{app}/secrets/{secret}", s.putSecret)
	auth("GET /api/apps/{app}/events", s.appEvents)
	auth("GET /api/runs/{id}", s.getRun)
	auth("POST /api/runs/{id}/cancel", s.cancelRun)
	auth("GET /api/runs/{id}/steps/{step}/log", s.stepLog)
	auth("GET /api/runs/{id}/events", s.runEvents)

	mux.Handle("/", s.ui())
	return s.guard(mux)
}

// guard adds security headers and rejects cross-site state changes. The
// session cookie is SameSite=Lax; the Origin check covers older browsers.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.URL.Path != "/api/webhooks/github" {
			if o := r.Header.Get("Origin"); o != "" && !sameOrigin(o, r) {
				http.Error(w, "cross-origin request refused", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func sameOrigin(origin string, r *http.Request) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := r.Host
	if fh := r.Header.Get("X-Forwarded-Host"); fh != "" {
		host = fh
	}
	return u.Host == host
}

// ---- sessions ----

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Server) requireSession(h func(http.ResponseWriter, *http.Request, string)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			httpError(w, http.StatusUnauthorized, "sign in with GitHub first")
			return
		}
		login, err := s.Store.SessionLogin(r.Context(), hashToken(c.Value))
		if err != nil || !s.allowed(login) {
			httpError(w, http.StatusUnauthorized, "session expired; sign in again")
			return
		}
		h(w, r, login)
	})
}

func (s *Server) allowed(login string) bool {
	return slices.ContainsFunc(s.AllowedUsers, func(u string) bool { return strings.EqualFold(u, login) })
}

func (s *Server) secureCookies() bool { return strings.HasPrefix(s.BaseURL, "https://") }

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	app, err := s.GitHub.Get()
	if err != nil {
		http.Redirect(w, r, "/setup", http.StatusFound)
		return
	}
	state := randomToken()
	http.SetCookie(w, &http.Cookie{Name: "oauth_state", Value: state, Path: "/api/auth", MaxAge: 600, HttpOnly: true, Secure: s.secureCookies(), SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, app.AuthorizeURL(state, s.BaseURL+"/api/auth/callback"), http.StatusFound)
}

func (s *Server) callback(w http.ResponseWriter, r *http.Request) {
	app, err := s.GitHub.Get()
	if err != nil {
		httpError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	st, err := r.Cookie("oauth_state")
	if err != nil || subtle.ConstantTimeCompare([]byte(st.Value), []byte(r.URL.Query().Get("state"))) != 1 {
		httpError(w, http.StatusBadRequest, "login state mismatch; try again")
		return
	}
	login, err := app.Login(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		s.Log.Warn("login failed", "err", err)
		httpError(w, http.StatusBadGateway, "GitHub login failed")
		return
	}
	if !s.allowed(login) {
		s.Log.Warn("login refused", "login", login)
		httpError(w, http.StatusForbidden, fmt.Sprintf("%s is not allowed to use this rendimiento instance", login))
		return
	}
	token := randomToken()
	if err := s.Store.CreateSession(r.Context(), hashToken(token), login, 7*24*time.Hour); err != nil {
		httpError(w, http.StatusInternalServerError, "could not create session")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", MaxAge: 7 * 24 * 3600, HttpOnly: true, Secure: s.secureCookies(), SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = s.Store.DeleteSession(r.Context(), hashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

// ---- one-click GitHub App setup (manifest flow) ----

func (s *Server) setupStatus(w http.ResponseWriter, _ *http.Request) {
	app, err := s.GitHub.Get()
	out := map[string]any{"configured": err == nil}
	if err == nil {
		c := app.Credentials()
		out["appSlug"], out["installURL"] = c.Slug, c.HTMLURL+"/installations/new"
	}
	writeJSON(w, out)
}

func (s *Server) checkSetupToken(w http.ResponseWriter, r *http.Request) bool {
	if _, err := s.GitHub.Get(); err == nil {
		httpError(w, http.StatusConflict, "GitHub App is already configured")
		return false
	}
	tok := r.URL.Query().Get("token")
	if c, err := r.Cookie("setup_token"); tok == "" && err == nil {
		tok = c.Value
	}
	if s.SetupToken == "" || subtle.ConstantTimeCompare([]byte(tok), []byte(s.SetupToken)) != 1 {
		httpError(w, http.StatusForbidden, "invalid setup token (see the rendimiento-setup secret)")
		return false
	}
	return true
}

var setupPage = template.Must(template.New("setup").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><title>Connecting GitHub</title></head>
<body style="font-family:system-ui;padding:2rem">
<p>Sending you to GitHub to create the <b>{{.Name}}</b> app…</p>
<form id="f" method="post" action="https://github.com/settings/apps/new?state={{.State}}">
<input type="hidden" name="manifest" value="{{.Manifest}}">
<button type="submit">Continue to GitHub</button>
</form>
<script>document.getElementById('f').submit()</script>
</body></html>`))

func (s *Server) setupStart(w http.ResponseWriter, r *http.Request) {
	if !s.checkSetupToken(w, r) {
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "setup_token", Value: s.SetupToken, Path: "/api/setup", MaxAge: 1800, HttpOnly: true, Secure: s.secureCookies(), SameSite: http.SameSiteLaxMode})
	manifest, _ := json.Marshal(gh.Manifest(s.AppName, s.BaseURL))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = setupPage.Execute(w, map[string]string{"Name": s.AppName, "Manifest": string(manifest), "State": randomToken()[:16]})
}

func (s *Server) setupCallback(w http.ResponseWriter, r *http.Request) {
	if !s.checkSetupToken(w, r) {
		return
	}
	creds, err := gh.ConvertManifest(r.Context(), s.GitHubAPI, r.URL.Query().Get("code"))
	if err != nil {
		s.Log.Error("manifest conversion", "err", err)
		httpError(w, http.StatusBadGateway, "GitHub did not accept the setup code; start setup again")
		return
	}
	app, err := gh.New(*creds)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.Credentials.Save(r.Context(), creds); err != nil {
		httpError(w, http.StatusInternalServerError, "could not store GitHub credentials: "+err.Error())
		return
	}
	s.GitHub.Set(app)
	s.Log.Info("GitHub App configured", "slug", creds.Slug)
	// Next: install the app on the repos to deploy.
	http.Redirect(w, r, creds.HTMLURL+"/installations/new", http.StatusFound)
}

// ---- webhooks ----

func (s *Server) webhook(w http.ResponseWriter, r *http.Request) {
	app, err := s.GitHub.Get()
	if err != nil {
		httpError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 25<<20))
	if err != nil {
		httpError(w, http.StatusBadRequest, "read body")
		return
	}
	if !gh.VerifyWebhook(app.Credentials().WebhookSecret, body, r.Header.Get("X-Hub-Signature-256")) {
		httpError(w, http.StatusUnauthorized, "bad signature")
		return
	}
	var payload struct {
		Ref          string `json:"ref"`
		After        string `json:"after"`
		Deleted      bool   `json:"deleted"`
		Installation struct {
			ID int64 `json:"id"`
		} `json:"installation"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	event := r.Header.Get("X-GitHub-Event")
	var push *platform.PushEvent
	switch event {
	case "push":
		if branch, ok := strings.CutPrefix(payload.Ref, "refs/heads/"); ok {
			push = &platform.PushEvent{Installation: payload.Installation.ID, Repo: payload.Repository.FullName, Branch: branch, SHA: payload.After, Deleted: payload.Deleted}
		}
	}
	// Pull requests need no event of their own: the branch push already ran
	// and its check run shows on the PR. PRs from forks are never built,
	// since their code would run with our clone token.
	if push == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if s.Addons != nil && strings.EqualFold(push.Repo, s.Addons.Repo) {
		s.Addons.Trigger() // add-on definitions (or their manifests) may have changed
	}
	runs, err := s.Platform.HandlePush(r.Context(), *push)
	if err != nil {
		s.Log.Error("webhook", "event", event, "err", err)
		httpError(w, http.StatusInternalServerError, "could not queue run")
		return
	}
	s.Log.Info("webhook", "event", event, "repo", push.Repo, "branch", push.Branch, "runs", len(runs))
	writeJSON(w, map[string]int{"queued": len(runs)})
}

// ---- GitHub browsing for the wizard ----

func (s *Server) installations(w http.ResponseWriter, r *http.Request, _ string) {
	app, err := s.GitHub.Get()
	if err != nil {
		httpError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	inst, err := app.Installations(r.Context())
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]any{"installations": inst, "installURL": app.Credentials().HTMLURL + "/installations/new"})
}

func (s *Server) repos(w http.ResponseWriter, r *http.Request, _ string) {
	app, err := s.GitHub.Get()
	if err != nil {
		httpError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpError(w, http.StatusBadRequest, "bad installation id")
		return
	}
	repos, err := app.Client(id).Repos(r.Context())
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, repos)
}

func (s *Server) zones(w http.ResponseWriter, r *http.Request, _ string) {
	zones, err := s.DNS.Zones(r.Context())
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	if zones == nil {
		zones = []string{}
	}
	if z := s.Platform.Config.Zone; z != "" && !slices.Contains(zones, z) {
		zones = append([]string{z}, zones...)
	}
	writeJSON(w, zones)
}

func (s *Server) propose(w http.ResponseWriter, r *http.Request, _ string) {
	var req struct {
		Installation int64  `json:"installation"`
		Repo         string `json:"repo"`
		Branch       string `json:"branch"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	prop, err := s.Platform.Propose(r.Context(), req.Installation, req.Repo, req.Branch)
	if err != nil {
		httpError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, prop)
}

// ---- services catalog ----

func (s *Server) services(w http.ResponseWriter, r *http.Request, _ string) {
	if s.Catalog == nil {
		httpError(w, http.StatusServiceUnavailable, "the services catalog is not enabled")
		return
	}
	c, err := s.Catalog.Get(r.Context(), r.URL.Query().Get("refresh") == "1")
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, c)
}

// ---- environment ----

func (s *Server) environment(w http.ResponseWriter, r *http.Request, _ string) {
	if s.Environment == nil {
		httpError(w, http.StatusServiceUnavailable, "environment checks are not enabled")
		return
	}
	writeJSON(w, s.Environment.Report(r.Context(), r.URL.Query().Get("refresh") == "1"))
}

// dnsSync re-detects the public IP now and repoints records that follow it.
func (s *Server) dnsSync(w http.ResponseWriter, r *http.Request, login string) {
	syncer, ok := s.DNS.(interface {
		Sync(context.Context) (*dns.SyncResult, error)
	})
	if !ok {
		httpError(w, http.StatusBadRequest, "the configured DNS provider does not support dynamic DNS")
		return
	}
	res, err := syncer.Sync(r.Context())
	if s.Environment != nil {
		s.Environment.Invalidate()
	}
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.Log.Info("dynamic DNS sync", "ip", res.IP, "changed", res.Changed, "records", res.Updated, "by", login)
	writeJSON(w, res)
}

// migration tells the wizard whether an app name matches a namespace that
// already runs something (so it can be migrated instead of duplicated).
func (s *Server) migration(w http.ResponseWriter, r *http.Request, _ string) {
	name := r.PathValue("name")
	if !dnsLabelRe.MatchString(name) {
		httpError(w, http.StatusBadRequest, "invalid name")
		return
	}
	m, err := s.Platform.InspectNamespace(r.Context(), name)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"migration": m})
}

var dnsLabelRe = regexp.MustCompile(`^[a-z]([-a-z0-9]{0,61}[a-z0-9])?$`)

// ---- apps ----

type appView struct {
	*store.App
	Status    v1alpha1.AppStatus `json:"status"`
	LastRun   *store.Run         `json:"lastRun,omitempty"`
	Suspended bool               `json:"suspended"`
	// Uptime24h is the share of successful uptime checks in the last 24
	// hours (absent before the first check).
	Uptime24h *float64 `json:"uptime24h,omitempty"`
}

func (s *Server) view(ctx context.Context, a *store.App) appView {
	v := appView{App: a}
	var cr v1alpha1.App
	if err := s.Kube.Get(ctx, client.ObjectKey{Name: a.Name}, &cr); err == nil {
		v.Status, v.Suspended = cr.Status, cr.Spec.Suspend
	}
	if runs, err := s.Store.ListRuns(ctx, a.ID, 1); err == nil && len(runs) > 0 {
		v.LastRun = runs[0]
	}
	return v
}

func (s *Server) listApps(w http.ResponseWriter, r *http.Request, _ string) {
	apps, err := s.Store.ListApps(r.Context())
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	uptime, err := s.Store.UptimeSummary(r.Context(), time.Now().Add(-24*time.Hour))
	if err != nil {
		s.Log.Warn("uptime summary", "err", err) // the list is still useful without it
	}
	out := make([]appView, 0, len(apps))
	for _, a := range apps {
		v := s.view(r.Context(), a)
		if u := uptime[a.ID]; u.Total > 0 {
			ratio := float64(u.OK) / float64(u.Total)
			v.Uptime24h = &ratio
		}
		out = append(out, v)
	}
	writeJSON(w, out)
}

func (s *Server) createApp(w http.ResponseWriter, r *http.Request, login string) {
	var req struct {
		platform.OnboardRequest
		Existing bool `json:"existing"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	res, err := s.Platform.Onboard(r.Context(), req.OnboardRequest, req.Existing)
	if err != nil {
		httpError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	s.Log.Info("app onboarded", "app", res.App.Name, "repo", res.App.Repo, "by", login)
	writeJSONStatus(w, http.StatusCreated, res)
}

func (s *Server) app(w http.ResponseWriter, r *http.Request) *store.App {
	a, err := s.Store.GetApp(r.Context(), r.PathValue("app"))
	if errors.Is(err, store.ErrNotFound) {
		httpError(w, http.StatusNotFound, "no such app")
		return nil
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return nil
	}
	return a
}

func (s *Server) getApp(w http.ResponseWriter, r *http.Request, _ string) {
	if a := s.app(w, r); a != nil {
		writeJSON(w, s.view(r.Context(), a))
	}
}

func (s *Server) deleteApp(w http.ResponseWriter, r *http.Request, login string) {
	a := s.app(w, r)
	if a == nil {
		return
	}
	if r.URL.Query().Get("confirm") != a.Name {
		httpError(w, http.StatusBadRequest, "pass ?confirm=<app name> to delete the app, its namespace and its DNS records")
		return
	}
	if err := s.Platform.DeleteApp(r.Context(), a); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Log.Info("app deleted", "app", a.Name, "by", login)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteImpact(w http.ResponseWriter, r *http.Request, _ string) {
	a := s.app(w, r)
	if a == nil {
		return
	}
	im, err := s.Platform.DeleteImpact(r.Context(), a)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, im)
}

// disconnectApp stops managing the app but leaves it running.
func (s *Server) disconnectApp(w http.ResponseWriter, r *http.Request, login string) {
	a := s.app(w, r)
	if a == nil {
		return
	}
	if r.URL.Query().Get("confirm") != a.Name {
		httpError(w, http.StatusBadRequest, "pass ?confirm=<app name> to disconnect the app")
		return
	}
	if err := s.Platform.DisconnectApp(r.Context(), a); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Log.Info("app disconnected", "app", a.Name, "by", login)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request, _ string) {
	a := s.app(w, r)
	if a == nil {
		return
	}
	runs, err := s.Store.ListRuns(r.Context(), a.ID, 50)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if runs == nil {
		runs = []*store.Run{}
	}
	writeJSON(w, runs)
}

func (s *Server) triggerRun(w http.ResponseWriter, r *http.Request, _ string) {
	a := s.app(w, r)
	if a == nil {
		return
	}
	var req struct {
		Branch string `json:"branch"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req)
	if req.Branch == "" {
		req.Branch = a.DefaultBranch
	}
	run, err := s.Platform.QueueRun(r.Context(), a, req.Branch, "", "manual")
	if err != nil {
		httpError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSONStatus(w, http.StatusCreated, run)
}

func (s *Server) listReleases(w http.ResponseWriter, r *http.Request, _ string) {
	a := s.app(w, r)
	if a == nil {
		return
	}
	rels, err := s.Store.ListReleases(r.Context(), a.ID, 50)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rels == nil {
		rels = []*store.Release{}
	}
	ids := make([]int64, len(rels))
	for i, rel := range rels {
		ids[i] = rel.ID
	}
	if tasks, err := s.Store.ReleaseTasks(r.Context(), ids); err == nil {
		for _, rel := range rels {
			rel.Tasks = tasks[rel.ID]
		}
	}
	writeJSON(w, rels)
}

func (s *Server) rollback(w http.ResponseWriter, r *http.Request, login string) {
	a := s.app(w, r)
	if a == nil {
		return
	}
	var req struct {
		Release int64 `json:"release"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	rel, err := s.Platform.Rollback(r.Context(), a, req.Release)
	if errors.Is(err, store.ErrNotFound) {
		httpError(w, http.StatusNotFound, "no such release")
		return
	}
	if err != nil && strings.Contains(err.Error(), "never deployed") {
		httpError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Log.Info("rollback", "app", a.Name, "to", req.Release, "new", rel.Number, "by", login)
	writeJSON(w, rel)
}

// Resource tree for the Argo-style view.
type resourceNode struct {
	Kind     string         `json:"kind"`
	Name     string         `json:"name"`
	Health   string         `json:"health"` // Healthy | Progressing | Degraded | Missing
	Info     []string       `json:"info,omitempty"`
	Children []resourceNode `json:"children,omitempty"`
}

func (s *Server) resources(w http.ResponseWriter, r *http.Request, _ string) {
	a := s.app(w, r)
	if a == nil {
		return
	}
	ctx := r.Context()
	ns := client.InNamespace(a.Name)
	sel := client.MatchingLabels{render.LabelApp: a.Name}
	var deps appsv1.DeploymentList
	var pods corev1.PodList
	var svcs corev1.ServiceList
	var ings networkingv1.IngressList
	var pvcs corev1.PersistentVolumeClaimList
	for _, l := range []client.ObjectList{&deps, &pods, &svcs, &ings, &pvcs} {
		if err := s.Kube.List(ctx, l, ns, sel); err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	root := resourceNode{Kind: "App", Name: a.Name, Health: "Healthy"}
	for _, svc := range a.Spec.Services {
		node := resourceNode{Kind: "Service", Name: svc.Name, Health: "Missing"}
		for _, d := range deps.Items {
			if d.Name != svc.Name {
				continue
			}
			dn := resourceNode{Kind: "Deployment", Name: d.Name, Health: deploymentHealth(&d),
				Info: []string{fmt.Sprintf("%d/%d ready", d.Status.ReadyReplicas, ptrOr(d.Spec.Replicas, 1))}}
			for _, p := range pods.Items {
				if p.Labels[render.LabelService] == svc.Name {
					dn.Children = append(dn.Children, podNode(&p))
				}
			}
			node.Health = dn.Health
			node.Children = append(node.Children, dn)
		}
		for _, sv := range svcs.Items {
			if sv.Name == svc.Name {
				node.Children = append(node.Children, resourceNode{Kind: "Service", Name: sv.Name, Health: "Healthy", Info: []string{sv.Spec.ClusterIP}})
			}
		}
		for _, ing := range ings.Items {
			if ing.Name == svc.Name {
				info := []string{}
				for _, rule := range ing.Spec.Rules {
					info = append(info, "https://"+rule.Host)
				}
				h := "Progressing"
				if len(ing.Status.LoadBalancer.Ingress) > 0 {
					h = "Healthy"
				}
				node.Children = append(node.Children, resourceNode{Kind: "Ingress", Name: ing.Name, Health: h, Info: info})
			}
		}
		for _, pvc := range pvcs.Items {
			if pvc.Name == svc.Name+"-data" {
				h := "Progressing"
				if pvc.Status.Phase == corev1.ClaimBound {
					h = "Healthy"
				}
				node.Children = append(node.Children, resourceNode{Kind: "Volume", Name: pvc.Name, Health: h, Info: []string{string(pvc.Status.Phase)}})
			}
		}
		if node.Health != "Healthy" && root.Health != "Degraded" {
			root.Health = node.Health
			if node.Health == "Missing" {
				root.Health = "Progressing"
			}
		}
		root.Children = append(root.Children, node)
	}
	writeJSON(w, root)
}

func deploymentHealth(d *appsv1.Deployment) string {
	for _, c := range d.Status.Conditions {
		if c.Type == appsv1.DeploymentProgressing && c.Reason == "ProgressDeadlineExceeded" {
			return "Degraded"
		}
	}
	want := ptrOr(d.Spec.Replicas, 1)
	if d.Status.ObservedGeneration >= d.Generation && d.Status.UpdatedReplicas == want && d.Status.ReadyReplicas == want {
		return "Healthy"
	}
	return "Progressing"
}

func podNode(p *corev1.Pod) resourceNode {
	n := resourceNode{Kind: "Pod", Name: p.Name, Health: "Progressing", Info: []string{string(p.Status.Phase)}}
	ready := false
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
			ready = true
		}
	}
	for _, cs := range p.Status.ContainerStatuses {
		if w := cs.State.Waiting; w != nil && (w.Reason == "CrashLoopBackOff" || w.Reason == "ImagePullBackOff" || w.Reason == "ErrImagePull") {
			n.Health = "Degraded"
			n.Info = append(n.Info, w.Reason)
		}
		if cs.RestartCount > 0 {
			n.Info = append(n.Info, fmt.Sprintf("%d restarts", cs.RestartCount))
		}
	}
	if ready {
		n.Health = "Healthy"
	}
	if p.Spec.NodeName != "" {
		n.Info = append(n.Info, "node "+p.Spec.NodeName)
	}
	return n
}

func ptrOr(p *int32, d int32) int32 {
	if p == nil {
		return d
	}
	return *p
}

// putSecret writes app secret values straight into the app's namespace;
// they are never stored in git, in Postgres, or returned by the API.
func (s *Server) putSecret(w http.ResponseWriter, r *http.Request, login string) {
	a := s.app(w, r)
	if a == nil {
		return
	}
	name := r.PathValue("secret")
	if !slices.Contains(a.Spec.SecretNames(), name) {
		httpError(w, http.StatusBadRequest, fmt.Sprintf("secret %q is not used by any service, job or task in rendimiento.yaml (secrets, secretEnv or secretFiles)", name))
		return
	}
	var data map[string]string
	if !readJSON(w, r, &data) {
		return
	}
	var ns corev1.Namespace
	if err := s.Kube.Get(r.Context(), client.ObjectKey{Name: a.Name}, &ns); err != nil || ns.Labels[render.LabelApp] != a.Name {
		httpError(w, http.StatusConflict, "the app's namespace does not exist yet; set secrets after the first deploy starts")
		return
	}
	sec := &corev1.Secret{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: a.Name, Labels: map[string]string{render.LabelApp: a.Name, render.LabelManagedBy: render.ManagedBy}},
		StringData: data,
	}
	var existing corev1.Secret
	err := s.Kube.Get(r.Context(), client.ObjectKeyFromObject(sec), &existing)
	switch {
	case apierrors.IsNotFound(err):
		err = s.Kube.Create(r.Context(), sec)
	case err == nil:
		existing.Data, existing.StringData = nil, data
		err = s.Kube.Update(r.Context(), &existing)
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Restart pods so the new values are picked up.
	var deps appsv1.DeploymentList
	_ = s.Kube.List(r.Context(), &deps, client.InNamespace(a.Name), client.MatchingLabels{render.LabelApp: a.Name})
	for i := range deps.Items {
		d := &deps.Items[i]
		patch := client.MergeFrom(d.DeepCopy())
		if d.Spec.Template.Annotations == nil {
			d.Spec.Template.Annotations = map[string]string{}
		}
		d.Spec.Template.Annotations["rendimiento.ai/restartedAt"] = time.Now().Format(time.RFC3339)
		_ = s.Kube.Patch(r.Context(), d, patch)
	}
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	s.Log.Info("secret updated", "app", a.Name, "secret", name, "keys", keys, "by", login)
	w.WriteHeader(http.StatusNoContent)
}

// ---- runs ----

func (s *Server) runID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpError(w, http.StatusBadRequest, "bad run id")
		return 0, false
	}
	return id, true
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request, _ string) {
	id, ok := s.runID(w, r)
	if !ok {
		return
	}
	run, err := s.Store.GetRun(r.Context(), id)
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

func (s *Server) cancelRun(w http.ResponseWriter, r *http.Request, _ string) {
	id, ok := s.runID(w, r)
	if !ok {
		return
	}
	if !s.Platform.Cancel(id) {
		run, err := s.Store.GetRun(r.Context(), id)
		if err == nil && run.Status == store.RunQueued {
			_ = s.Store.FinishRun(r.Context(), id, store.RunCancelled, "cancelled before it started")
		} else {
			httpError(w, http.StatusConflict, "run is not running on this replica or already finished")
			return
		}
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) stepLog(w http.ResponseWriter, r *http.Request, _ string) {
	id, ok := s.runID(w, r)
	if !ok {
		return
	}
	log, ref, err := s.Store.StepLog(r.Context(), id, r.PathValue("step"))
	if errors.Is(err, store.ErrNotFound) {
		httpError(w, http.StatusNotFound, "no such step")
		return
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if ref != "" {
		log = s.archivedLog(r.Context(), ref)
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	io.WriteString(w, log)
}

func (s *Server) runEvents(w http.ResponseWriter, r *http.Request, _ string) {
	id, ok := s.runID(w, r)
	if !ok {
		return
	}
	s.sse(w, r, platform.RunTopic(id))
}

func (s *Server) appEvents(w http.ResponseWriter, r *http.Request, _ string) {
	if a := s.app(w, r); a != nil {
		s.sse(w, r, platform.AppTopic(a.Name))
	}
}

func (s *Server) sse(w http.ResponseWriter, r *http.Request, topic string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	ch, cancel := s.Platform.Hub.Subscribe(topic)
	defer cancel()
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no") // ingress-nginx: do not buffer
	io.WriteString(w, ": connected\n\n")
	flusher.Flush()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			io.WriteString(w, ": ping\n\n")
			flusher.Flush()
		case e, ok := <-ch:
			if !ok {
				return
			}
			b, _ := json.Marshal(e.Data)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, b)
			flusher.Flush()
		}
	}
}

// ---- UI ----

func (s *Server) ui() http.Handler {
	if s.UI == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, "rendimiento.ai API is running; the web UI was not built into this binary.")
		})
	}
	files := http.FileServerFS(s.UI)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			httpError(w, http.StatusNotFound, "no such endpoint")
			return
		}
		// Single-page app: unknown paths get index.html.
		p := strings.TrimPrefix(r.URL.Path, "/")
		if _, err := fs.Stat(s.UI, p); p == "" || err != nil {
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		} else if strings.HasPrefix(p, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, v any) { writeJSONStatus(w, http.StatusOK, v) }

// writeJSONStatus sets headers before the status line; headers set after
// WriteHeader are silently dropped.
func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

func httpError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
