package ruta

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// fakeEncargos keeps encargos in memory with the store's state rules.
type fakeEncargos struct {
	mu   sync.Mutex
	list []store.Encargo
	keys map[int64]string
}

func (f *fakeEncargos) find(id int64) (*store.Encargo, error) {
	for i := range f.list {
		if f.list[i].ID == id {
			return &f.list[i], nil
		}
	}
	return nil, store.ErrNotFound
}

func (f *fakeEncargos) step(e *store.Encargo, who, what, note string) store.Encargo {
	e.Log = append(e.Log, store.EncargoLog{At: time.Now(), Who: who, What: what, Note: note})
	e.UpdatedAt = time.Now()
	if e.KeyID != nil {
		e.KeyName = f.keys[*e.KeyID]
	} else {
		e.KeyName = ""
	}
	return *e
}

func (f *fakeEncargos) CreateEncargo(_ context.Context, e store.Encargo) (store.Encargo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e.ID, e.Status, e.Links = int64(len(f.list)+1), store.EncargoPending, []string{}
	f.list = append(f.list, e)
	return f.step(&f.list[len(f.list)-1], e.CreatedBy, "created", ""), nil
}
func (f *fakeEncargos) Encargo(_ context.Context, id int64) (store.Encargo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, err := f.find(id)
	if err != nil {
		return store.Encargo{}, err
	}
	return *e, nil
}
func (f *fakeEncargos) ListEncargos(_ context.Context, _ int64, openOnly bool, _ int) ([]store.Encargo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []store.Encargo
	for _, e := range f.list {
		if !openOnly || (e.Status != store.EncargoReviewed && e.Status != store.EncargoCancelled) {
			out = append(out, e)
		}
	}
	return out, nil
}

// move changes an encargo when its state (and owner, if given) allow it.
func (f *fakeEncargos) move(id int64, owner *int64, from []string, apply func(*store.Encargo), who, what, note string) (store.Encargo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, err := f.find(id)
	if err != nil {
		return store.Encargo{}, err
	}
	ok := false
	for _, s := range from {
		ok = ok || e.Status == s
	}
	if !ok || (owner != nil && (e.KeyID == nil || *e.KeyID != *owner)) {
		return store.Encargo{}, store.ErrEncargoState
	}
	apply(e)
	return f.step(e, who, what, note), nil
}

func (f *fakeEncargos) TakeEncargo(_ context.Context, id, keyID, cargoID int64, who string) (store.Encargo, error) {
	return f.move(id, nil, []string{store.EncargoPending}, func(e *store.Encargo) {
		e.Status, e.KeyID, e.CargoID = store.EncargoTaken, &keyID, &cargoID
	}, who, "taken", "")
}
func (f *fakeEncargos) WaitEncargo(_ context.Context, id, keyID, runID int64, who string) (store.Encargo, error) {
	return f.move(id, &keyID, []string{store.EncargoTaken, store.EncargoWaiting}, func(e *store.Encargo) {
		e.Status, e.WaitingRun = store.EncargoWaiting, &runID
	}, who, "waiting", "")
}
func (f *fakeEncargos) ResumeEncargo(_ context.Context, id, keyID int64, who, note string) (store.Encargo, error) {
	return f.move(id, &keyID, []string{store.EncargoWaiting}, func(e *store.Encargo) {
		e.Status, e.WaitingRun = store.EncargoTaken, nil
	}, who, "resumed", note)
}
func (f *fakeEncargos) DeliverEncargo(_ context.Context, id, keyID int64, who, result string, links []string) (store.Encargo, error) {
	return f.move(id, &keyID, []string{store.EncargoTaken, store.EncargoWaiting}, func(e *store.Encargo) {
		e.Status, e.Result, e.Links, e.WaitingRun = store.EncargoDelivered, result, links, nil
	}, who, "delivered", "")
}
func (f *fakeEncargos) ReleaseEncargo(_ context.Context, id, keyID int64, who, note string) (store.Encargo, error) {
	return f.move(id, &keyID, []string{store.EncargoTaken, store.EncargoWaiting}, func(e *store.Encargo) {
		e.Status, e.KeyID, e.CargoID, e.WaitingRun = store.EncargoPending, nil, nil, nil
	}, who, "released", note)
}

// An agent finds a pending encargo at the start, takes it (and nobody else
// can), tests a branch, waits on the run (woken when it ends) and hands the
// encargo over with links. A reader only sees them.
func TestEncargoTools(t *testing.T) {
	m := &memory{keys: map[string]store.AgentKey{}}
	tokens := map[string]string{}
	fe := &fakeEncargos{keys: map[int64]string{}}
	for i, k := range []store.AgentKey{
		{Name: "uno", Role: store.RoleCollaborator},
		{Name: "dos", Role: store.RoleCollaborator},
		{Name: "lector", Role: store.RoleReader},
	} {
		tok, h, err := NewToken()
		if err != nil {
			t.Fatal(err)
		}
		k.ID, k.ExpiresAt = int64(i+1), time.Now().Add(time.Hour)
		m.keys[h] = k
		tokens[k.Name] = tok
		fe.keys[k.ID] = k.Name
	}
	fp := &fakePlataforma{
		apps: []*store.App{{ID: 1, Name: "shop", DefaultBranch: "main"}},
		runs: map[int64]*store.Run{},
	}
	if _, err := fe.CreateEncargo(context.Background(), store.Encargo{Title: "Arreglar el carrito", Body: "falla con 500", App: "shop", CreatedBy: "p0dxD"}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer((&Server{Store: m, Plataforma: fp, Encargos: fe}).Handler())
	defer srv.Close()

	uno, err := connect(t, srv.URL, tokens["uno"])
	if err != nil {
		t.Fatal(err)
	}
	defer uno.Close()
	out, _ := call(t, uno, "ruta_inicio", map[string]any{})
	if p, _ := out["encargos_pendientes"].([]any); len(p) != 1 || p[0].(map[string]any)["titulo"] != "Arreglar el carrito" {
		t.Fatalf("inicio = %v", out["encargos_pendientes"])
	}
	if _, msg := call(t, uno, "encargo_tomar", map[string]any{"id": 1}); !strings.Contains(msg, "cargo") {
		t.Fatalf("taking without a cargo: %q", msg)
	}
	call(t, uno, "cargo_tomar", map[string]any{"proposito": "Arreglar el carrito de shop"})
	out, msg := call(t, uno, "encargo_tomar", map[string]any{"id": 1})
	if msg != "" || out["estado"] != "tomado" || out["agente"] != "uno" {
		t.Fatalf("taken = %v %q", out, msg)
	}

	dos, _ := connect(t, srv.URL, tokens["dos"])
	defer dos.Close()
	call(t, dos, "cargo_tomar", map[string]any{"proposito": "Ver qué hay"})
	if _, msg := call(t, dos, "encargo_tomar", map[string]any{"id": 1}); !strings.Contains(msg, "lo tiene uno") {
		t.Fatalf("taken twice: %q", msg)
	}
	if _, msg := call(t, dos, "encargo_entregar", map[string]any{"id": 1, "resultado": "x"}); !strings.Contains(msg, "lo tiene uno") {
		t.Fatalf("delivered by another: %q", msg)
	}

	// Test a branch and wait: the run's end wakes the call.
	out, _ = call(t, uno, "ejecucion_lanzar", map[string]any{"app": "shop", "rama": "carrito"})
	run := int64(out["id"].(float64))
	go func() {
		time.Sleep(100 * time.Millisecond)
		fp.finish(run, store.RunSucceeded)
	}()
	out, msg = call(t, uno, "encargo_esperar", map[string]any{"id": 1, "ejecucion": run, "segundos": 30})
	if msg != "" || out["terminada"] != true || out["ejecucion"].(map[string]any)["estado"] != store.RunSucceeded || out["encargo"].(map[string]any)["estado"] != "tomado" {
		t.Fatalf("waited = %v %q", out, msg)
	}

	out, msg = call(t, uno, "encargo_entregar", map[string]any{"id": 1, "resultado": "Arreglado en la rama carrito; la ejecución pasó.",
		"enlaces": []string{"https://github.com/o/shop/pull/new/carrito"}})
	if msg != "" || out["estado"] != "entregado" {
		t.Fatalf("delivered = %v %q", out, msg)
	}
	out, _ = call(t, uno, "encargo_leer", map[string]any{"id": 1})
	var steps []string
	for _, h := range out["historia"].([]any) {
		steps = append(steps, h.(map[string]any)["que"].(string))
	}
	if got := strings.Join(steps, ", "); got != "creado, tomado, esperando, terminó la espera, entregado" {
		t.Fatalf("history = %s", got)
	}

	// A reader sees encargos but cannot take or create them.
	lector, _ := connect(t, srv.URL, tokens["lector"])
	defer lector.Close()
	out, msg = call(t, lector, "encargos_listar", map[string]any{})
	if msg != "" || len(out["encargos"].([]any)) != 1 {
		t.Fatalf("reader's list = %v %q", out, msg)
	}
	res, err := lector.CallTool(context.Background(), &mcp.CallToolParams{Name: "encargo_tomar", Arguments: map[string]any{"id": 1}})
	if err == nil && !res.IsError {
		t.Fatal("a reader took an encargo")
	}
}
