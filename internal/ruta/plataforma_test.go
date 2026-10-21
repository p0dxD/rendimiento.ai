package ruta

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/p0dxD/rendimiento.ai/internal/spec"
	"github.com/p0dxD/rendimiento.ai/internal/store"
)

type fakePlataforma struct {
	apps      []*store.App
	runs      map[int64]*store.Run
	queued    []string // app + " " + branch
	cancelled []int64
}

func (f *fakePlataforma) ListApps(context.Context) ([]*store.App, error) { return f.apps, nil }
func (f *fakePlataforma) GetApp(_ context.Context, name string) (*store.App, error) {
	for _, a := range f.apps {
		if a.Name == name {
			return a, nil
		}
	}
	return nil, store.ErrNotFound
}
func (f *fakePlataforma) GetAppByID(_ context.Context, id int64) (*store.App, error) {
	for _, a := range f.apps {
		if a.ID == id {
			return a, nil
		}
	}
	return nil, store.ErrNotFound
}
func (f *fakePlataforma) ListRuns(_ context.Context, appID int64, _ int) ([]*store.Run, error) {
	var out []*store.Run
	for _, r := range f.runs {
		if r.AppID == appID {
			out = append(out, r)
		}
	}
	return out, nil
}
func (f *fakePlataforma) GetRun(_ context.Context, id int64) (*store.Run, error) {
	if r, ok := f.runs[id]; ok {
		return r, nil
	}
	return nil, store.ErrNotFound
}
func (f *fakePlataforma) ListReleases(context.Context, int64, int) ([]*store.Release, error) {
	return []*store.Release{{Number: 7, SHA: "abcdef123456", VerifyStatus: store.VerifyPassed}}, nil
}
func (f *fakePlataforma) StepLog(_ context.Context, run int64, step string) (string, error) {
	if step != "web:test" {
		return "", store.ErrNotFound
	}
	var b strings.Builder
	for i := 1; i <= 300; i++ {
		fmt.Fprintf(&b, "línea %d\n", i)
	}
	return b.String(), nil
}
func (f *fakePlataforma) ListTraces(context.Context, store.TraceQuery) ([]store.Trace, error) {
	return []store.Trace{{ID: "t1", Services: []string{"shop/web"}}, {ID: "t2", Services: []string{"blog/web"}}}, nil
}
func (f *fakePlataforma) GetTrace(_ context.Context, id string) (*store.Trace, []store.Span, error) {
	if id == "t2" {
		return &store.Trace{ID: "t2", Services: []string{"blog/web"}}, nil, nil
	}
	return nil, nil, store.ErrNotFound
}
func (f *fakePlataforma) ListProblems(context.Context, string, time.Time, bool, int) ([]store.Problem, error) {
	return []store.Problem{{Level: "error", Component: "verify", Message: "web did not answer", Count: 2}}, nil
}
func (f *fakePlataforma) QueueRun(_ context.Context, a *store.App, branch string) (*store.Run, error) {
	f.queued = append(f.queued, a.Name+" "+branch)
	r := &store.Run{ID: 99, AppID: a.ID, Branch: branch, Status: store.RunQueued}
	f.runs[r.ID] = r
	return r, nil
}
func (f *fakePlataforma) CancelRun(_ context.Context, id int64) error {
	f.cancelled = append(f.cancelled, id)
	f.runs[id].Status = store.RunCancelled
	return nil
}

// Agents read the platform (apps, runs, logs, kept requests) and, with a
// collaborator key and a cargo, test a branch or cancel a run; never the
// default branch, which deploys. A key limited to some apps sees only those.
func TestPlataformaTools(t *testing.T) {
	m := &memory{keys: map[string]store.AgentKey{}}
	tokens := map[string]string{}
	for i, k := range []store.AgentKey{
		{Name: "colaborador", Role: store.RoleCollaborator},
		{Name: "lector", Role: store.RoleReader},
		{Name: "solo-shop", Role: store.RoleCollaborator, Apps: []string{"shop"}},
	} {
		tok, h, err := NewToken()
		if err != nil {
			t.Fatal(err)
		}
		k.ID, k.ExpiresAt = int64(i+1), time.Now().Add(time.Hour)
		m.keys[h] = k
		tokens[k.Name] = tok
	}
	fp := &fakePlataforma{
		apps: []*store.App{
			{ID: 1, Name: "shop", Repo: "o/shop", DefaultBranch: "main", Spec: spec.Spec{Services: []spec.Service{{Name: "web"}}}},
			{ID: 2, Name: "blog", Repo: "o/blog", DefaultBranch: "main"},
		},
		runs: map[int64]*store.Run{
			10: {ID: 10, AppID: 1, Branch: "main", SHA: "1234567890", Status: store.RunFailed, Steps: []store.Step{{ID: "web:test", Kind: "test", Status: "failed"}}},
			20: {ID: 20, AppID: 2, Branch: "main", Status: store.RunSucceeded},
		},
	}
	srv := httptest.NewServer((&Server{Store: m, Plataforma: fp}).Handler())
	defer srv.Close()
	session := func(name string) *mcp.ClientSession {
		s, err := connect(t, srv.URL, tokens[name])
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	}

	// A reader sees the reading tools only.
	lector := session("lector")
	res, err := lector.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
	}
	all := strings.Join(names, ",")
	if !strings.Contains(all, "apps_listar") || !strings.Contains(all, "paso_registro") || strings.Contains(all, "ejecucion_lanzar") {
		t.Fatalf("reader's tools = %s", all)
	}

	col := session("colaborador")
	out, msg := call(t, col, "apps_listar", map[string]any{})
	if msg != "" || len(out["apps"].([]any)) != 2 {
		t.Fatalf("apps = %v %s", out, msg)
	}
	out, msg = call(t, col, "app_estado", map[string]any{"app": "shop"})
	if msg != "" || len(out["servicios"].([]any)) != 1 || len(out["problemas"].([]any)) != 1 {
		t.Fatalf("estado = %v %s", out, msg)
	}
	out, msg = call(t, col, "ejecucion_leer", map[string]any{"id": 10})
	if msg != "" || out["estado"] != store.RunFailed || out["commit"] != "1234567" {
		t.Fatalf("run = %v %s", out, msg)
	}
	out, msg = call(t, col, "paso_registro", map[string]any{"ejecucion": 10, "paso": "web:test", "lineas": 50})
	if msg != "" || out["cortado"] != true || !strings.HasPrefix(out["registro"].(string), "línea 251\n") || !strings.HasSuffix(out["registro"].(string), "línea 300") {
		t.Fatalf("log = %v %s", out, msg)
	}

	// Acting needs a cargo, and never the default branch.
	if _, msg := call(t, col, "ejecucion_lanzar", map[string]any{"app": "shop", "rama": "arreglo"}); !strings.Contains(msg, "cargo") {
		t.Fatalf("without a cargo: %q", msg)
	}
	call(t, col, "cargo_tomar", map[string]any{"proposito": "Probar el arreglo de shop"})
	if _, msg := call(t, col, "ejecucion_lanzar", map[string]any{"app": "shop", "rama": "main"}); !strings.Contains(msg, "producción") {
		t.Fatalf("the default branch: %q", msg)
	}
	out, msg = call(t, col, "ejecucion_lanzar", map[string]any{"app": "shop", "rama": "arreglo"})
	if msg != "" || out["id"] != float64(99) || strings.Join(fp.queued, ";") != "shop arreglo" {
		t.Fatalf("queued = %v %s %v", out, msg, fp.queued)
	}
	out, msg = call(t, col, "ejecucion_cancelar", map[string]any{"id": 99})
	if msg != "" || out["estado"] != store.RunCancelled || len(fp.cancelled) != 1 {
		t.Fatalf("cancel = %v %s", out, msg)
	}

	// A key limited to shop sees and acts on shop only.
	shop := session("solo-shop")
	out, _ = call(t, shop, "apps_listar", map[string]any{})
	if apps := out["apps"].([]any); len(apps) != 1 || apps[0].(map[string]any)["nombre"] != "shop" {
		t.Fatalf("limited apps = %v", apps)
	}
	if _, msg := call(t, shop, "ejecucion_leer", map[string]any{"id": 20}); !strings.Contains(msg, "no tiene acceso") {
		t.Fatalf("another app's run: %q", msg)
	}
	out, _ = call(t, shop, "solicitudes_listar", map[string]any{})
	if list := out["solicitudes"].([]any); len(list) != 1 {
		t.Fatalf("limited requests = %v", list)
	}
	if _, msg := call(t, shop, "solicitud_leer", map[string]any{"id": "t2"}); !strings.Contains(msg, "no tiene acceso") {
		t.Fatalf("another app's request: %q", msg)
	}
}
