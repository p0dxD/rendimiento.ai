package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/p0dxD/rendimiento.ai/api/v1alpha1"
	"github.com/p0dxD/rendimiento.ai/internal/dns"
	"github.com/p0dxD/rendimiento.ai/internal/events"
	gh "github.com/p0dxD/rendimiento.ai/internal/github"
	"github.com/p0dxD/rendimiento.ai/internal/platform"
	"github.com/p0dxD/rendimiento.ai/internal/render"
	"github.com/p0dxD/rendimiento.ai/internal/spec"
	"github.com/p0dxD/rendimiento.ai/internal/store"
)

type memCreds struct{ c *gh.Credentials }

func (m *memCreds) Load(context.Context) (*gh.Credentials, error)   { return m.c, nil }
func (m *memCreds) Save(_ context.Context, c *gh.Credentials) error { m.c = c; return nil }

type testEnv struct {
	srv   *httptest.Server
	s     *Server
	kube  client.Client
	store *store.Store
}

func newEnv(t *testing.T, withApp bool) *testEnv {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := st.ResetForTests(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)
	kube := fake.NewClientBuilder().WithScheme(scheme).Build()

	holder := &gh.Holder{}
	if withApp {
		key, _ := rsa.GenerateKey(rand.Reader, 2048)
		pemKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
		app, err := gh.New(gh.Credentials{AppID: 1, PrivateKey: string(pemKey), WebhookSecret: "whsec", ClientID: "cid", HTMLURL: "https://github.com/apps/x"})
		if err != nil {
			t.Fatal(err)
		}
		holder.Set(app)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	p := &platform.Platform{Store: st, Kube: kube, GitHub: holder, Hub: events.NewHub(), Log: log}
	s := &Server{
		Platform: p, Store: st, Kube: kube, GitHub: holder, Credentials: &memCreds{}, DNS: dns.Noop{}, Log: log,
		BaseURL: "https://rendimiento.example", AllowedUsers: []string{"p0dxD"}, SetupToken: "setup-123", AppName: "rendimiento-test",
		UI: fstest.MapFS{"index.html": {Data: []byte("<html>ui</html>")}, "assets/app.js": {Data: []byte("js")}},
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return &testEnv{srv: srv, s: s, kube: kube, store: st}
}

// session creates a logged-in cookie for login directly in the store.
func (e *testEnv) session(t *testing.T, login string) *http.Cookie {
	tok := randomToken()
	if err := e.store.CreateSession(context.Background(), hashToken(tok), login, 3600e9); err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: sessionCookie, Value: tok}
}

func (e *testEnv) do(t *testing.T, method, path, body string, cookie *http.Cookie, hdr map[string]string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	if cookie != nil {
		req.AddCookie(cookie)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(b)
}

func TestAuthBoundaries(t *testing.T) {
	e := newEnv(t, true)
	if r, _ := e.do(t, "GET", "/api/apps", "", nil, nil); r.StatusCode != 401 {
		t.Fatalf("no session: %d", r.StatusCode)
	}
	if r, _ := e.do(t, "GET", "/api/apps", "", &http.Cookie{Name: sessionCookie, Value: "forged"}, nil); r.StatusCode != 401 {
		t.Fatalf("forged session: %d", r.StatusCode)
	}
	// A valid session for a user later removed from the allowlist is refused.
	if r, _ := e.do(t, "GET", "/api/apps", "", e.session(t, "mallory"), nil); r.StatusCode != 401 {
		t.Fatalf("non-allowlisted: %d", r.StatusCode)
	}
	me := e.session(t, "P0DXD")
	if r, body := e.do(t, "GET", "/api/me", "", me, nil); r.StatusCode != 200 || !strings.Contains(body, "P0DXD") {
		t.Fatalf("me: %d %s", r.StatusCode, body)
	}
	// Cross-site state change refused even with a valid cookie.
	if r, _ := e.do(t, "POST", "/api/apps/x/rollback", `{"release":1}`, me, map[string]string{"Origin": "https://evil.example"}); r.StatusCode != 403 {
		t.Fatalf("cross-origin: %d", r.StatusCode)
	}
	if r, _ := e.do(t, "GET", "/healthz", "", nil, nil); r.StatusCode != 200 {
		t.Fatal("healthz")
	}
	// Login with a bad state is refused.
	if r, _ := e.do(t, "GET", "/api/auth/callback?code=x&state=y", "", &http.Cookie{Name: "oauth_state", Value: "z"}, nil); r.StatusCode != 400 {
		t.Fatalf("oauth state: %d", r.StatusCode)
	}
}

func sign(secret, body string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func TestWebhookSignature(t *testing.T) {
	e := newEnv(t, true)
	body := `{"ref":"refs/heads/main","after":"` + strings.Repeat("a", 40) + `","repository":{"full_name":"p0dxD/nothing"},"installation":{"id":1}}`
	if r, _ := e.do(t, "POST", "/api/webhooks/github", body, nil, map[string]string{"X-GitHub-Event": "push", "X-Hub-Signature-256": sign("wrong", body)}); r.StatusCode != 401 {
		t.Fatalf("bad signature accepted: %d", r.StatusCode)
	}
	r, out := e.do(t, "POST", "/api/webhooks/github", body, nil, map[string]string{"X-GitHub-Event": "push", "X-Hub-Signature-256": sign("whsec", body)})
	if r.StatusCode != 200 || !strings.Contains(out, `"queued":0`) {
		t.Fatalf("valid webhook: %d %s", r.StatusCode, out)
	}
	pr := `{"action":"opened","repository":{"full_name":"p0dxD/x"}}`
	if r, _ := e.do(t, "POST", "/api/webhooks/github", pr, nil, map[string]string{"X-GitHub-Event": "pull_request", "X-Hub-Signature-256": sign("whsec", pr)}); r.StatusCode != 202 {
		t.Fatalf("pull_request should be acknowledged and ignored: %d", r.StatusCode)
	}
}

func TestSetupRequiresToken(t *testing.T) {
	e := newEnv(t, false)
	if r, body := e.do(t, "GET", "/api/setup/status", "", nil, nil); r.StatusCode != 200 || !strings.Contains(body, `"configured":false`) {
		t.Fatalf("status: %s", body)
	}
	if r, _ := e.do(t, "GET", "/api/setup/github?token=nope", "", nil, nil); r.StatusCode != 403 {
		t.Fatalf("bad setup token: %d", r.StatusCode)
	}
	r, body := e.do(t, "GET", "/api/setup/github?token=setup-123", "", nil, nil)
	if r.StatusCode != 200 || !strings.Contains(body, "https://github.com/settings/apps/new") || !strings.Contains(body, "rendimiento.example/api/webhooks/github") {
		t.Fatalf("setup page: %d %s", r.StatusCode, body)
	}
	if r, _ := e.do(t, "GET", "/api/auth/login", "", nil, nil); r.StatusCode != 302 || r.Header.Get("Location") != "/setup" {
		t.Fatalf("login before setup should go to /setup: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	// Webhooks are refused until configured.
	if r, _ := e.do(t, "POST", "/api/webhooks/github", "{}", nil, nil); r.StatusCode != 503 {
		t.Fatalf("webhook before setup: %d", r.StatusCode)
	}
}

func TestSecretsAndDelete(t *testing.T) {
	e := newEnv(t, true)
	ctx := context.Background()
	me := e.session(t, "p0dxD")
	sp := spec.Spec{Services: []spec.Service{{Name: "web", Secrets: []string{"web-env"}}}}
	sp.Default()
	app := &store.App{Name: "shop", Repo: "p0dxD/shop", DefaultBranch: "main", Spec: sp}
	if err := e.store.CreateApp(ctx, app); err != nil {
		t.Fatal(err)
	}
	// Undeclared secret names are refused, so the API cannot write arbitrary secrets.
	if r, _ := e.do(t, "PUT", "/api/apps/shop/secrets/other", `{"A":"1"}`, me, nil); r.StatusCode != 400 {
		t.Fatalf("undeclared secret: %d", r.StatusCode)
	}
	// Namespace missing → conflict; foreign namespace → conflict.
	if r, _ := e.do(t, "PUT", "/api/apps/shop/secrets/web-env", `{"A":"1"}`, me, nil); r.StatusCode != 409 {
		t.Fatalf("no namespace: %d", r.StatusCode)
	}
	_ = e.kube.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "shop", Labels: map[string]string{render.LabelApp: "shop"}}})
	if r, body := e.do(t, "PUT", "/api/apps/shop/secrets/web-env", `{"API_KEY":"s3cret"}`, me, nil); r.StatusCode != 204 {
		t.Fatalf("put secret: %d %s", r.StatusCode, body)
	}
	var sec corev1.Secret
	if err := e.kube.Get(ctx, client.ObjectKey{Namespace: "shop", Name: "web-env"}, &sec); err != nil || sec.StringData["API_KEY"] != "s3cret" {
		t.Fatalf("secret = %+v %v", sec, err)
	}
	// Delete requires typing the app name.
	if r, _ := e.do(t, "DELETE", "/api/apps/shop", "", me, nil); r.StatusCode != 400 {
		t.Fatalf("delete without confirm: %d", r.StatusCode)
	}
	if r, _ := e.do(t, "DELETE", "/api/apps/shop?confirm=shop", "", me, nil); r.StatusCode != 204 {
		t.Fatalf("delete: %d", r.StatusCode)
	}
	if r, _ := e.do(t, "GET", "/api/apps/shop", "", me, nil); r.StatusCode != 404 {
		t.Fatalf("after delete: %d", r.StatusCode)
	}
}

func TestUIFallback(t *testing.T) {
	e := newEnv(t, true)
	if _, body := e.do(t, "GET", "/apps/shop/runs/3", "", nil, nil); !strings.Contains(body, "<html>ui</html>") {
		t.Fatalf("SPA fallback: %s", body)
	}
	if r, _ := e.do(t, "GET", "/assets/app.js", "", nil, nil); !strings.Contains(r.Header.Get("Cache-Control"), "immutable") {
		t.Fatal("assets should be cached")
	}
	if r, _ := e.do(t, "GET", "/api/nope", "", nil, nil); r.StatusCode != 404 {
		t.Fatalf("unknown api: %d", r.StatusCode)
	}
	var m map[string]string
	_, body := e.do(t, "GET", "/api/nope", "", nil, nil)
	if json.Unmarshal([]byte(body), &m) != nil || m["error"] == "" {
		t.Fatalf("api errors should be JSON: %s", body)
	}
}

// Regression: 201 responses were sent without Content-Type because the
// header was set after WriteHeader, and the UI then read them as text.
func TestJSONStatusKeepsContentType(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSONStatus(rec, http.StatusCreated, map[string]string{"ok": "yes"})
	if rec.Code != 201 || rec.Result().Header.Get("Content-Type") != "application/json" {
		t.Fatalf("status %d, content-type %q", rec.Code, rec.Result().Header.Get("Content-Type"))
	}
}

func TestMigrationCheckByName(t *testing.T) {
	e := newEnv(t, true)
	ctx := context.Background()
	me := e.session(t, "p0dxD")
	if r, _ := e.do(t, "GET", "/api/namespaces/podoi/migration", "", nil, nil); r.StatusCode != 401 {
		t.Fatalf("unauthenticated: %d", r.StatusCode)
	}
	if r, _ := e.do(t, "GET", "/api/namespaces/Not_Valid/migration", "", me, nil); r.StatusCode != 400 {
		t.Fatalf("invalid name: %d", r.StatusCode)
	}
	if r, body := e.do(t, "GET", "/api/namespaces/podoi/migration", "", me, nil); r.StatusCode != 200 || !strings.Contains(body, `"migration":null`) {
		t.Fatalf("absent namespace: %d %s", r.StatusCode, body)
	}
	_ = e.kube.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "podoi"}})
	if r, body := e.do(t, "GET", "/api/namespaces/podoi/migration", "", me, nil); r.StatusCode != 200 || !strings.Contains(body, `"namespace":"podoi"`) {
		t.Fatalf("existing namespace: %d %s", r.StatusCode, body)
	}
}

// Public stats are off unless turned on, need no login when on, allow only
// the configured origins, and say nothing private.
func TestPublicStats(t *testing.T) {
	e := newEnv(t, false)
	if resp, _ := e.do(t, "GET", "/api/public/stats", "", nil, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("off: %d", resp.StatusCode)
	}
	e.s.PublicStats, e.s.PublicOrigins, e.s.PublicTimeZone = true, []string{"https://joserod.space"}, "America/New_York"
	resp, body := e.do(t, "GET", "/api/public/stats", "", nil, map[string]string{"Origin": "https://joserod.space"})
	if resp.StatusCode != 200 || resp.Header.Get("Access-Control-Allow-Origin") != "https://joserod.space" {
		t.Fatalf("on: %d %q", resp.StatusCode, resp.Header.Get("Access-Control-Allow-Origin"))
	}
	var st PublicStats
	if err := json.Unmarshal([]byte(body), &st); err != nil || st.Delivery == nil || st.TimeZone != "America/New_York" || st.Sites == nil {
		t.Fatalf("body = %s (%v)", body, err)
	}
	if resp, _ := e.do(t, "GET", "/api/public/stats", "", nil, map[string]string{"Origin": "https://evil.example"}); resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("an unlisted origin was allowed")
	}
	if strings.Contains(body, `"version"`) || strings.Contains(body, `"os"`) || strings.Contains(body, `"name":"worker`) {
		t.Fatalf("public stats reveal versions, OS or node names: %s", body)
	}

	// Internal only: the public listener no longer serves them (and does not
	// fall through to the UI), the internal handler does.
	internal := &Server{Store: e.s.Store, Log: e.s.Log, PublicStats: true, StatsInternalOnly: true,
		UI: e.s.UI, AllowedUsers: e.s.AllowedUsers, Kube: e.s.Kube, Platform: e.s.Platform, GitHub: e.s.GitHub, Credentials: e.s.Credentials, DNS: e.s.DNS}
	pub := httptest.NewServer(internal.Handler())
	defer pub.Close()
	if resp, err := http.Get(pub.URL + "/api/public/stats"); err != nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("public listener with StatsInternalOnly: %v %v", resp.StatusCode, err)
	}
	priv := httptest.NewServer(internal.StatsHandler())
	defer priv.Close()
	if resp, err := http.Get(priv.URL + "/api/public/stats"); err != nil || resp.StatusCode != 200 {
		t.Fatalf("internal listener: %v %v", resp.StatusCode, err)
	}
}
