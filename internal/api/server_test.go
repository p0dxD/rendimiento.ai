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
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/p0dxD/rendimiento.ai/api/v1alpha1"
	"github.com/p0dxD/rendimiento.ai/internal/analytics"
	"github.com/p0dxD/rendimiento.ai/internal/dns"
	"github.com/p0dxD/rendimiento.ai/internal/events"
	gh "github.com/p0dxD/rendimiento.ai/internal/github"
	"github.com/p0dxD/rendimiento.ai/internal/platform"
	"github.com/p0dxD/rendimiento.ai/internal/render"
	"github.com/p0dxD/rendimiento.ai/internal/ruta"
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
	if _, err := st.SeedAdmins(ctx, []string{"p0dxD"}); err != nil {
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
	e.s.BookURL, e.s.BookURLEs = "https://learn.example.com/", "https://aprende.example.com/"
	if _, body := e.do(t, "GET", "/api/setup/status", "", nil, nil); !strings.Contains(body, `"book":{"en":"https://learn.example.com/","es":"https://aprende.example.com/"}`) {
		t.Fatalf("the welcome page links the book: %s", body)
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
	// A second save adds its keys and keeps the others; null removes a key.
	if r, body := e.do(t, "PUT", "/api/apps/shop/secrets/web-env", `{"DB_URL":"postgres://x"}`, me, nil); r.StatusCode != 204 {
		t.Fatalf("add key: %d %s", r.StatusCode, body)
	}
	var listed struct{ Keys []string }
	_, body := e.do(t, "GET", "/api/apps/shop/secrets/web-env", "", me, nil)
	if json.Unmarshal([]byte(body), &listed) != nil || strings.Contains(body, "s3cret") || !slices.Equal(listed.Keys, []string{"API_KEY", "DB_URL"}) {
		t.Fatalf("keys after adding = %v (%s)", listed.Keys, body)
	}
	if r, body := e.do(t, "PUT", "/api/apps/shop/secrets/web-env", `{"API_KEY":null}`, me, nil); r.StatusCode != 204 {
		t.Fatalf("remove key: %d %s", r.StatusCode, body)
	}
	sec = corev1.Secret{}
	if err := e.kube.Get(ctx, client.ObjectKey{Namespace: "shop", Name: "web-env"}, &sec); err != nil || string(sec.Data["DB_URL"]) != "postgres://x" || sec.Data["API_KEY"] != nil {
		t.Fatalf("after removing API_KEY = %+v %v", sec.Data, err)
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

func TestUniverse(t *testing.T) {
	e := newEnv(t, true)
	ctx := context.Background()
	me := e.session(t, "p0dxD")
	sp := spec.Spec{Services: []spec.Service{
		{Name: "web", Needs: []spec.Need{{Kind: spec.NeedService, Service: "api"}}},
		{Name: "api", Needs: []spec.Need{{Kind: spec.NeedPostgres}}},
	}}
	sp.Default()
	if err := e.store.CreateApp(ctx, &store.App{Name: "shop", Repo: "p0dxD/shop", DefaultBranch: "main", Spec: sp}); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.do(t, "GET", "/api/universe", "", nil, nil); r.StatusCode != 401 {
		t.Fatalf("without a session: %d", r.StatusCode)
	}
	r, body := e.do(t, "GET", "/api/universe", "", me, nil)
	var u struct {
		Systems []struct{ ID, Kind string }
		Nodes   []struct{ ID, Kind string }
		Edges   []struct{ From, To string }
	}
	if r.StatusCode != 200 || json.Unmarshal([]byte(body), &u) != nil {
		t.Fatalf("universe: %d %s", r.StatusCode, body)
	}
	if len(u.Systems) != 1 || u.Systems[0].Kind != "app" || len(u.Nodes) != 3 || len(u.Edges) != 2 ||
		u.Edges[0].From != "shop/api" || u.Edges[0].To != "shop/postgres" || u.Edges[1].From != "shop/web" || u.Edges[1].To != "shop/api" {
		t.Fatalf("universe = %s", body)
	}
	// Without a metrics store, a service's spans are an empty list.
	if r, body := e.do(t, "GET", "/api/universe/spans?service=shop/api", "", me, nil); r.StatusCode != 200 || !strings.Contains(body, `"spans":[]`) || !strings.Contains(body, `"traces":false`) {
		t.Fatalf("spans: %d %s", r.StatusCode, body)
	}
	if r, _ := e.do(t, "GET", "/api/universe/spans", "", me, nil); r.StatusCode != 400 {
		t.Fatalf("spans without a service: %d", r.StatusCode)
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

// The dashboard's delivery report needs a login and has both periods and
// twelve weeks.
func TestDeliveryReport(t *testing.T) {
	e := newEnv(t, true)
	if r, _ := e.do(t, "GET", "/api/stats/delivery", "", nil, nil); r.StatusCode != 401 {
		t.Fatalf("without a session: %d", r.StatusCode)
	}
	e.s.PublicTimeZone = "America/New_York"
	r, body := e.do(t, "GET", "/api/stats/delivery", "", e.session(t, "p0dxD"), nil)
	var rep DeliveryReport
	if r.StatusCode != 200 || json.Unmarshal([]byte(body), &rep) != nil {
		t.Fatalf("%d %s", r.StatusCode, body)
	}
	if rep.Days != 30 || rep.TimeZone != "America/New_York" || rep.Current == nil || rep.Previous == nil || len(rep.Weekly) != 12 || len(rep.Current.DeploysPerDay) != 30 {
		t.Fatalf("report = %s", body)
	}
}

// The Problems page needs a login; problems can be listed, counted and dismissed.
func TestProblemsAPI(t *testing.T) {
	e := newEnv(t, true)
	ctx := context.Background()
	if r, _ := e.do(t, "GET", "/api/problems", "", nil, nil); r.StatusCode != 401 {
		t.Fatalf("without a session: %d", r.StatusCode)
	}
	_ = e.s.Store.RecordProblem(ctx, store.Problem{Fingerprint: "x", Level: "WARN", App: "shop", Message: "rejected", LastAt: time.Now()})
	me := e.session(t, "p0dxD")
	r, body := e.do(t, "GET", "/api/problems?app=shop", "", me, nil)
	var list ProblemList
	if r.StatusCode != 200 || json.Unmarshal([]byte(body), &list) != nil || list.Open != 1 || len(list.Problems) != 1 || list.OpenFor != 24 {
		t.Fatalf("%d %s", r.StatusCode, body)
	}
	if r, body := e.do(t, "GET", "/api/problems/count", "", me, nil); r.StatusCode != 200 || !strings.Contains(body, `"open":1`) {
		t.Fatalf("count: %d %s", r.StatusCode, body)
	}
	if r, _ := e.do(t, "POST", fmt.Sprintf("/api/problems/%d/dismiss", list.Problems[0].ID), "", me, nil); r.StatusCode != 204 {
		t.Fatalf("dismiss: %d", r.StatusCode)
	}
	if r, _ := e.do(t, "POST", "/api/problems/424242/dismiss", "", me, nil); r.StatusCode != 404 {
		t.Fatalf("dismiss unknown: %d", r.StatusCode)
	}
	if _, body := e.do(t, "GET", "/api/problems/count", "", me, nil); !strings.Contains(body, `"open":0`) {
		t.Fatalf("after dismissing: %s", body)
	}
}

// A browser that asks for Spanish gets the platform's messages in Spanish;
// names and English requests are left alone.
func TestSpanishAnswers(t *testing.T) {
	e := newEnv(t, true)
	ctx := context.Background()
	_ = e.s.Store.RecordProblem(ctx, store.Problem{Fingerprint: "n", Level: "WARN", Component: "notify", App: "Nodes",
		Message: "notification email failed", Resolution: "", LastAt: time.Now()})
	me := e.session(t, "p0dxD")
	_, es := e.do(t, "GET", "/api/problems", "", me, map[string]string{"Accept-Language": "es-MX,es;q=0.9"})
	if !strings.Contains(es, `"message":"falló el correo de notificación"`) || !strings.Contains(es, `"app":"Nodes"`) {
		t.Fatalf("Spanish answer: %s", es)
	}
	_, en := e.do(t, "GET", "/api/problems", "", me, map[string]string{"Accept-Language": "en-US"})
	if !strings.Contains(en, `"message":"notification email failed"`) {
		t.Fatalf("English answer: %s", en)
	}
}

func TestRutaAPI(t *testing.T) {
	e := newEnv(t, false)
	e.s.MCP = (&ruta.Server{Store: e.store, Log: e.s.Log}).Handler()
	srv := httptest.NewServer(e.s.Handler())
	t.Cleanup(srv.Close)
	e.srv = srv
	me := e.session(t, "p0dxD")

	if r, _ := e.do(t, "GET", "/api/ruta", "", nil, nil); r.StatusCode != 401 {
		t.Fatalf("without a session: %d", r.StatusCode)
	}
	r, body := e.do(t, "POST", "/api/ruta", `{"kind":"vivido","title":"La escuela","body":"Uniformes, recreos en el centro de la escuela.","tags":[" Cotija "]}`, me, nil)
	if r.StatusCode != 201 || !strings.Contains(body, `"author":"p0dxD"`) || !strings.Contains(body, `"tags":["cotija"]`) {
		t.Fatalf("add: %d %s", r.StatusCode, body)
	}
	if r, body := e.do(t, "POST", "/api/ruta", `{"kind":"nota","title":"x","body":"ghp_abcdefghijklmnopqrstuvwxyz0123456789"}`, me, nil); r.StatusCode != 400 || !strings.Contains(body, "secret") {
		t.Fatalf("secret: %d %s", r.StatusCode, body)
	}
	if r, body := e.do(t, "POST", "/api/ruta", `{"kind":"nota","title":"x","body":"ghp_abcdefghijklmnopqrstuvwxyz0123456789"}`, me, map[string]string{"Accept-Language": "es"}); !strings.Contains(body, "no guarda secretos") {
		t.Fatalf("secret, in Spanish: %d %s", r.StatusCode, body)
	}

	r, body = e.do(t, "POST", "/api/agent-keys", `{"name":"claude en main","canWrite":true,"days":30}`, me, nil)
	var created CreatedAgentKey
	if r.StatusCode != 201 || json.Unmarshal([]byte(body), &created) != nil || !strings.HasPrefix(created.Token, "rnd_") || created.Endpoint != "https://rendimiento.example/mcp" {
		t.Fatalf("create key: %d %s", r.StatusCode, body)
	}
	if r, _ := e.do(t, "POST", "/api/agent-keys", `{"name":"x","days":365}`, me, nil); r.StatusCode != 400 {
		t.Fatalf("a year-long key: %d", r.StatusCode)
	}
	if _, body := e.do(t, "GET", "/api/agent-keys", "", me, nil); strings.Contains(body, created.Token) || strings.Contains(body, "token_hash") {
		t.Fatalf("the key list leaks the token: %s", body)
	}

	// The agent uses the key over MCP: takes a cargo, writes, hands over.
	c := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
	sess, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp",
		HTTPClient: &http.Client{Transport: bearerRT{created.Token}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"cargo_tomar", map[string]any{"proposito": "probar"}},
		{"ruta_anotar", map[string]any{"tipo": "pendiente", "titulo": "Copiar respaldos a R2", "cuerpo": "cuando haya token"}},
		{"cargo_entregar", map[string]any{"entrega": "anoté el pendiente"}},
	} {
		res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: tc.tool, Arguments: tc.args})
		if err != nil || res.IsError {
			t.Fatalf("%s: %v %+v", tc.tool, err, res)
		}
	}
	if _, body := e.do(t, "GET", "/api/ruta?kind=pendiente", "", me, nil); !strings.Contains(body, `"byAgent":true`) || !strings.Contains(body, "claude en main") {
		t.Fatalf("the agent's entry: %s", body)
	}
	if _, body := e.do(t, "GET", "/api/ruta/activity", "", me, nil); !strings.Contains(body, "anoté el pendiente") || !strings.Contains(body, "ruta_anotar") {
		t.Fatalf("activity: %s", body)
	}

	// A browser on another site cannot use /mcp, even with a token.
	req, _ := http.NewRequest("POST", srv.URL+"/mcp", strings.NewReader(`{}`))
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Authorization", "Bearer "+created.Token)
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != 403 {
		t.Fatalf("cross-origin /mcp: %v %v", res, err)
	}

	// Revoked: the token stops working.
	if r, _ := e.do(t, "DELETE", fmt.Sprintf("/api/agent-keys/%d", created.Key.ID), "", me, nil); r.StatusCode != 204 {
		t.Fatalf("revoke: %d", r.StatusCode)
	}
	if _, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "ruta_inicio"}); err == nil {
		t.Fatal("a revoked key still works")
	}
}

type bearerRT struct{ token string }

func (b bearerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// fakeAnalytics counts one website, for the shop app's domain.
type fakeAnalytics struct{}

func (fakeAnalytics) Websites(context.Context) ([]analytics.Website, error) {
	return []analytics.Website{{ID: "w1", Name: "Shop", Domain: "www.shop.example.com"}, {ID: "w2", Name: "Other", Domain: "other.example.com"}}, nil
}

func (fakeAnalytics) Report(_ context.Context, w analytics.Website, p analytics.Period) (*analytics.Report, error) {
	return &analytics.Report{Website: w, Current: analytics.Stats{Visitors: 7, Pageviews: 20}, Pageviews: []analytics.Point{{At: p.From, N: 20}}}, nil
}

func (fakeAnalytics) Totals(context.Context, analytics.Website, time.Time, time.Time) (analytics.Stats, analytics.Stats, error) {
	return analytics.Stats{Visitors: 7}, analytics.Stats{Visitors: 5}, nil
}

func TestVisitsAPI(t *testing.T) {
	e := newEnv(t, false)
	ctx := context.Background()
	me := e.session(t, "p0dxD")
	sp := spec.Spec{Services: []spec.Service{{Name: "web", Domain: "shop.example.com"}}}
	sp.Default()
	if err := e.store.CreateApp(ctx, &store.App{Name: "shop", Repo: "p0dxD/shop", DefaultBranch: "main", Spec: sp}); err != nil {
		t.Fatal(err)
	}
	// Without Umami the tab says so instead of failing.
	if r, body := e.do(t, "GET", "/api/apps/shop/visits", "", me, nil); r.StatusCode != 200 || !strings.Contains(body, `"configured":false`) {
		t.Fatalf("off: %d %s", r.StatusCode, body)
	}
	e.s.Analytics = fakeAnalytics{}
	if r, _ := e.do(t, "GET", "/api/apps/shop/visits", "", nil, nil); r.StatusCode != 401 {
		t.Fatalf("without a session: %d", r.StatusCode)
	}
	if r, _ := e.do(t, "GET", "/api/apps/shop/visits?range=1y", "", me, nil); r.StatusCode != 400 {
		t.Fatalf("bad range: %d", r.StatusCode)
	}
	r, body := e.do(t, "GET", "/api/apps/shop/visits?range=24h", "", me, nil)
	var v VisitsView
	if r.StatusCode != 200 || json.Unmarshal([]byte(body), &v) != nil || len(v.Reports) != 1 || v.Reports[0].Website.ID != "w1" {
		t.Fatalf("visits: %d %s", r.StatusCode, body)
	}
	r, body = e.do(t, "GET", "/api/stats/visits", "", me, nil)
	var sum VisitsSummary
	if r.StatusCode != 200 || json.Unmarshal([]byte(body), &sum) != nil || len(sum.Apps) != 1 || sum.Current.Visitors != 7 || sum.Previous.Visitors != 5 {
		t.Fatalf("summary: %d %s", r.StatusCode, body)
	}
}

// The welcome page's activity is off unless asked for, and names only the app.
func TestPublicActivity(t *testing.T) {
	e := newEnv(t, false)
	ctx := context.Background()
	if r, _ := e.do(t, "GET", "/api/public/activity", "", nil, nil); r.StatusCode != http.StatusNotFound {
		t.Fatalf("off: %d", r.StatusCode)
	}
	sp := spec.Spec{Services: []spec.Service{{Name: "web"}}}
	sp.Default()
	app := &store.App{Name: "shop", Repo: "p0dxD/secret-repo", DefaultBranch: "main", Spec: sp}
	if err := e.store.CreateApp(ctx, app); err != nil {
		t.Fatal(err)
	}
	run := &store.Run{AppID: app.ID, SHA: "abc123def", Branch: "feature-x", Event: "pull_request"}
	if err := e.store.CreateRun(ctx, run, nil); err != nil {
		t.Fatal(err)
	}
	e.s.PublicActivity = true
	r, body := e.do(t, "GET", "/api/public/activity", "", nil, nil)
	var a PublicActivity
	if r.StatusCode != 200 || json.Unmarshal([]byte(body), &a) != nil || len(a.Running) != 1 || a.Running[0].App != "shop" ||
		a.Running[0].Status != store.RunQueued || a.Running[0].Deploy || a.Recent == nil {
		t.Fatalf("%d %s", r.StatusCode, body)
	}
	for _, private := range []string{"secret-repo", "abc123", "feature-x", "pull_request"} {
		if strings.Contains(body, private) {
			t.Fatalf("public activity reveals %q: %s", private, body)
		}
	}
	if _, body := e.do(t, "GET", "/api/setup/status", "", nil, nil); !strings.Contains(body, `"activity":true`) {
		t.Fatalf("the welcome page learns the activity is on: %s", body)
	}
}

func TestLoginStartsOnBaseURL(t *testing.T) {
	s := &Server{BaseURL: "https://rendimiento.example"}
	for host, want := range map[string]string{
		"rendimiento.example":     "",
		"RENDIMIENTO.example":     "",
		"old.rendimiento.example": "https://rendimiento.example/api/auth/login",
	} {
		r := httptest.NewRequest("GET", "/api/auth/login", nil)
		r.Host = host
		if got := s.loginElsewhere(r); got != want {
			t.Errorf("login on %s: redirect %q, want %q", host, got, want)
		}
	}
}

// People visiting an old address go to BaseURL with the same path; what
// machines call there (webhooks, MCP, public stats, health) still answers.
func TestRedirectHosts(t *testing.T) {
	s := &Server{BaseURL: "https://rendimiento.example", RedirectHosts: []string{"old.example"}}
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "here") })
	h := s.redirectHosts(ok)
	for _, c := range []struct{ method, host, path, want string }{
		{"GET", "old.example", "/apps/shop/runs/7?tab=log", "https://rendimiento.example/apps/shop/runs/7?tab=log"},
		{"GET", "OLD.example:443", "/", "https://rendimiento.example/"},
		{"GET", "rendimiento.example", "/apps/shop", ""},
		{"POST", "old.example", "/api/webhooks/github", ""},
		{"POST", "old.example", "/mcp", ""},
		{"GET", "old.example", "/mcp", ""},
		{"GET", "old.example", "/api/public/stats", ""},
		{"GET", "old.example", "/healthz", ""},
		{"POST", "old.example", "/api/runs/1/cancel", ""},
	} {
		r := httptest.NewRequest(c.method, c.path, nil)
		r.Host = c.host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if got := w.Header().Get("Location"); got != c.want || (c.want != "" && w.Code != http.StatusPermanentRedirect) {
			t.Errorf("%s %s%s: %d to %q, want %q", c.method, c.host, c.path, w.Code, got, c.want)
		}
	}
	if (&Server{}).redirectHosts(ok) == nil {
		t.Error("no hosts: the handler itself")
	}
}

// /llms.txt tells an agent how to connect, without naming apps or people.
func TestLLMsTxt(t *testing.T) {
	s := &Server{BaseURL: "https://rendimiento.example/", BookURL: "https://book.example"}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/llms.txt", nil))
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, "https://rendimiento.example/mcp") || !strings.Contains(body, "https://book.example/guide/agents/") || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/markdown") {
		t.Fatalf("%d %s\n%s", w.Code, w.Header().Get("Content-Type"), body)
	}
}

// A person asks agents for something (from a failed run, the app is filled
// in), and reviews what comes back: sending it back needs a comment.
func TestEncargosAPI(t *testing.T) {
	e := newEnv(t, false)
	srv := httptest.NewServer(e.s.Handler())
	t.Cleanup(srv.Close)
	e.srv = srv
	me := e.session(t, "p0dxD")
	ctx := context.Background()

	if r, _ := e.do(t, "GET", "/api/encargos", "", nil, nil); r.StatusCode != 401 {
		t.Fatalf("without a session: %d", r.StatusCode)
	}
	if r, _ := e.do(t, "POST", "/api/encargos", `{"title":""}`, me, nil); r.StatusCode != 400 {
		t.Fatalf("no title: %d", r.StatusCode)
	}
	r, body := e.do(t, "POST", "/api/encargos", `{"title":"Revisar el carrito","body":"responde 500","app":"nada"}`, me, nil)
	if r.StatusCode != 400 {
		t.Fatalf("an unknown app: %d %s", r.StatusCode, body)
	}
	r, body = e.do(t, "POST", "/api/encargos", `{"title":"Revisar el carrito","body":"responde 500"}`, me, nil)
	var created store.Encargo
	if r.StatusCode != 201 || json.Unmarshal([]byte(body), &created) != nil || created.Status != store.EncargoPending || created.CreatedBy != "p0dxD" {
		t.Fatalf("create: %d %s", r.StatusCode, body)
	}

	// An agent takes it and hands it over (the MCP side, here through the store).
	k, err := e.store.CreateAgentKey(ctx, store.DefaultOrg, "claude", "h", store.RoleCollaborator, nil, "p0dxD", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	c, _ := e.store.TakeCargo(ctx, k.ID, "el carrito")
	if _, err := e.store.TakeEncargo(ctx, created.ID, k.ID, c.ID, "claude"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.DeliverEncargo(ctx, created.ID, k.ID, "claude", "arreglado", nil); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/encargos/%d", created.ID)
	if r, _ := e.do(t, "POST", path+"/review", `{"accept":false}`, me, nil); r.StatusCode != 400 {
		t.Fatalf("sent back without a comment: %d", r.StatusCode)
	}
	r, body = e.do(t, "POST", path+"/review", `{"accept":false,"comment":"falta una prueba"}`, me, nil)
	if r.StatusCode != 200 || !strings.Contains(body, `"status":"pending"`) || !strings.Contains(body, "falta una prueba") {
		t.Fatalf("sent back: %d %s", r.StatusCode, body)
	}
	if r, _ := e.do(t, "POST", path+"/review", `{"accept":true}`, me, nil); r.StatusCode != 409 {
		t.Fatalf("reviewing one that is not delivered: %d", r.StatusCode)
	}
	if r, body := e.do(t, "POST", path+"/cancel", "", me, nil); r.StatusCode != 200 || !strings.Contains(body, `"status":"cancelled"`) {
		t.Fatalf("cancel: %d %s", r.StatusCode, body)
	}
	if _, body := e.do(t, "GET", "/api/encargos", "", me, nil); body != "[]\n" && body != "[]" {
		t.Fatalf("open after cancelling: %s", body)
	}
	if r, body := e.do(t, "GET", path, "", me, nil); r.StatusCode != 200 || !strings.Contains(body, `"what":"returned"`) {
		t.Fatalf("history: %d %s", r.StatusCode, body)
	}
}

// Every route takes at least the role it declares: a guest may read, but
// not act or administer; a collaborator may act, but not administer; a
// stranger with a session is not let in at all.
func TestRouteRoles(t *testing.T) {
	e := newEnv(t, false)
	ctx := context.Background()
	for login, role := range map[string]string{"invitada": store.MemberGuest, "colabora": store.MemberCollaborator} {
		if err := e.store.SetMember(ctx, store.DefaultOrg, login, role, "p0dxD"); err != nil {
			t.Fatal(err)
		}
	}
	guest, collab := e.session(t, "invitada"), e.session(t, "colabora")
	fill := strings.NewReplacer("{app}", "nada", "{id}", "1", "{name}", "nada", "{addon}", "nada", "{number}", "1",
		"{task}", "nada", "{secret}", "nada", "{step}", "nada", "{login}", "nadie")
	checked := 0
	for _, rt := range e.s.routes {
		method, path, _ := strings.Cut(rt.pattern, " ")
		path = fill.Replace(path)
		for who, c := range map[string]*http.Cookie{store.MemberGuest: guest, store.MemberCollaborator: collab} {
			// Live routes (/events) stream until the client leaves: one that
			// is still open after 3 seconds got past the role check.
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			req, _ := http.NewRequestWithContext(ctx, method, e.srv.URL+path, strings.NewReader("{}"))
			req.AddCookie(c)
			status, body := 0, ""
			if res, err := http.DefaultClient.Do(req); err == nil {
				b, _ := io.ReadAll(res.Body)
				res.Body.Close()
				status, body = res.StatusCode, string(b)
			}
			cancel()
			allowed := store.RoleRank(who) >= store.RoleRank(rt.role)
			if !allowed && status != http.StatusForbidden {
				t.Errorf("%s as %s: %d, want 403 (%s)", rt.pattern, who, status, body)
			}
			if allowed && status == http.StatusForbidden && strings.Contains(body, "role") {
				t.Errorf("%s as %s: refused by role, but the route allows %s", rt.pattern, who, rt.role)
			}
			checked++
		}
	}
	if checked < 150 {
		t.Fatalf("only %d checks: are the routes registered?", checked)
	}
	if r, _ := e.do(t, "GET", "/api/me", "", e.session(t, "extraña"), nil); r.StatusCode != 401 {
		t.Fatalf("a stranger: %d", r.StatusCode)
	}
	if _, body := e.do(t, "GET", "/api/me", "", guest, nil); !strings.Contains(body, `"role":"guest"`) {
		t.Fatalf("me: %s", body)
	}
}

// An administrator adds and removes people; the last administrator stays.
func TestMembersAPI(t *testing.T) {
	e := newEnv(t, false)
	me := e.session(t, "p0dxD")
	if r, _ := e.do(t, "PUT", "/api/members/no%20vale", `{"role":"guest"}`, me, nil); r.StatusCode != 400 {
		t.Fatalf("a bad login: %d", r.StatusCode)
	}
	if r, _ := e.do(t, "PUT", "/api/members/ana", `{"role":"jefa"}`, me, nil); r.StatusCode != 400 {
		t.Fatalf("a bad role: %d", r.StatusCode)
	}
	if r, body := e.do(t, "PUT", "/api/members/ana", `{"role":"guest"}`, me, nil); r.StatusCode != 200 || !strings.Contains(body, `"login":"ana"`) {
		t.Fatalf("add: %d %s", r.StatusCode, body)
	}
	if r, _ := e.do(t, "PUT", "/api/members/p0dxD", `{"role":"guest"}`, me, nil); r.StatusCode != 409 {
		t.Fatalf("demoting the last admin: %d", r.StatusCode)
	}
	if r, _ := e.do(t, "DELETE", "/api/members/ana", "", me, nil); r.StatusCode != 204 {
		t.Fatalf("remove: %d", r.StatusCode)
	}
	if r, _ := e.do(t, "DELETE", "/api/members/ana", "", me, nil); r.StatusCode != 404 {
		t.Fatalf("remove again: %d", r.StatusCode)
	}
}

// Two organizations: nothing of one is seen from the other (apps, runs,
// the Ruta, encargos, agent keys, members), the platform's routes are for
// its operators only, and a person switches between their organizations.
func TestOrgIsolation(t *testing.T) {
	e := newEnv(t, false)
	ctx := context.Background()
	sp := spec.Spec{Services: []spec.Service{{Name: "web"}}}
	sp.Default()
	app := &store.App{Name: "tienda", Repo: "p0dxD/tienda", DefaultBranch: "main", Spec: sp}
	if err := e.store.CreateApp(ctx, app); err != nil {
		t.Fatal(err)
	}
	run := &store.Run{AppID: app.ID, SHA: "abc123def", Branch: "main", Event: "push"}
	if err := e.store.CreateRun(ctx, run, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.AddRuta(ctx, store.RutaEntry{Kind: store.KindNota, Title: "secreto de la principal", Author: "p0dxD"}); err != nil {
		t.Fatal(err)
	}
	enc, err := e.store.CreateEncargo(ctx, store.Encargo{Title: "de la principal", CreatedBy: "p0dxD"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateAgentKey(ctx, store.DefaultOrg, "llave principal", "h-p", store.RoleCollaborator, nil, "p0dxD", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	// The operator makes an organization for someone else.
	me := e.session(t, "p0dxD")
	r, body := e.do(t, "POST", "/api/orgs", `{"slug":"otra","name":"Otra"}`, me, nil)
	var otra store.Org
	if r.StatusCode != 201 || json.Unmarshal([]byte(body), &otra) != nil {
		t.Fatalf("create org: %d %s", r.StatusCode, body)
	}
	if r, _ := e.do(t, "POST", "/api/orgs", `{"slug":"otra","name":"Otra vez"}`, me, nil); r.StatusCode != 409 {
		t.Fatalf("a taken address: %d", r.StatusCode)
	}
	if err := e.store.SetMember(ctx, otra.ID, "ana", store.MemberAdmin, "p0dxD"); err != nil {
		t.Fatal(err)
	}
	ana := e.session(t, "ana") // not in the principal one: lands in hers

	if _, body := e.do(t, "GET", "/api/me", "", ana, nil); !strings.Contains(body, `"slug":"otra"`) || !strings.Contains(body, `"operator":false`) {
		t.Fatalf("me: %s", body)
	}
	for path, leak := range map[string]string{
		"/api/apps":            "tienda",
		"/api/ruta":            "secreto de la principal",
		"/api/encargos":        "de la principal",
		"/api/agent-keys":      "llave principal",
		"/api/members":         "p0dxD",
		"/api/universe/traces": "tienda",
	} {
		r, body := e.do(t, "GET", path, "", ana, nil)
		if r.StatusCode != 200 || strings.Contains(body, leak) {
			t.Errorf("%s from the other organization: %d %s", path, r.StatusCode, body)
		}
	}
	for _, path := range []string{"/api/apps/tienda", fmt.Sprintf("/api/runs/%d", run.ID), fmt.Sprintf("/api/encargos/%d", enc.ID)} {
		if r, _ := e.do(t, "GET", path, "", ana, nil); r.StatusCode != 404 {
			t.Errorf("%s from the other organization: %d, want 404", path, r.StatusCode)
		}
	}
	for _, path := range []string{"/api/environment", "/api/problems", "/api/mercado", "/api/installed"} {
		if r, _ := e.do(t, "GET", path, "", ana, nil); r.StatusCode != 403 {
			t.Errorf("platform route %s for another organization's admin: %d, want 403", path, r.StatusCode)
		}
	}
	if r, _ := e.do(t, "POST", "/api/orgs", `{"slug":"tercera","name":"Tercera"}`, ana, nil); r.StatusCode != 403 {
		t.Fatalf("only operators make organizations: %d", r.StatusCode)
	}
	if r, _ := e.do(t, "POST", "/api/orgs/1/switch", "", ana, nil); r.StatusCode != 404 {
		t.Fatalf("switching to an organization she is not in: %d", r.StatusCode)
	}

	// The operator is in both, and switches.
	if _, body := e.do(t, "GET", "/api/orgs", "", me, nil); !strings.Contains(body, `"slug":"principal"`) || !strings.Contains(body, `"slug":"otra"`) {
		t.Fatalf("operator's orgs: %s", body)
	}
	if r, _ := e.do(t, "POST", fmt.Sprintf("/api/orgs/%d/switch", otra.ID), "", me, nil); r.StatusCode != 204 {
		t.Fatalf("switch: %d", r.StatusCode)
	}
	if _, body := e.do(t, "GET", "/api/apps", "", me, nil); strings.Contains(body, "tienda") {
		t.Fatalf("after switching, the principal's apps: %s", body)
	}
	if r, _ := e.do(t, "GET", "/api/problems", "", me, nil); r.StatusCode != 200 {
		t.Fatalf("the operator keeps the platform's routes in any organization: %d", r.StatusCode)
	}
}
