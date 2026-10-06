package ruta

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// memory is an in-memory Backend.
type memory struct {
	mu      sync.Mutex
	keys    map[string]store.AgentKey // by hash
	cargos  []store.Cargo
	entries []store.RutaEntry
	actions []store.AgentAction
}

func (m *memory) AgentKeyByHash(_ context.Context, h string) (store.AgentKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.keys[h]
	if !ok {
		return k, store.ErrNotFound
	}
	return k, nil
}

func (m *memory) keyName(id int64) string {
	for _, k := range m.keys {
		if k.ID == id {
			return k.Name
		}
	}
	return ""
}

func (m *memory) TakeCargo(_ context.Context, keyID int64, purpose string) (store.Cargo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.cargos {
		if c.KeyID == keyID && c.EndedAt == nil {
			return c, store.ErrCargoOpen
		}
	}
	c := store.Cargo{ID: int64(len(m.cargos) + 1), KeyID: keyID, KeyName: m.keyName(keyID), Purpose: purpose, StartedAt: time.Now()}
	m.cargos = append(m.cargos, c)
	return c, nil
}

func (m *memory) OpenCargo(_ context.Context, keyID int64) (store.Cargo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.cargos {
		if c.KeyID == keyID && c.EndedAt == nil {
			return c, nil
		}
	}
	return store.Cargo{}, store.ErrNotFound
}

func (m *memory) HandOver(_ context.Context, id int64, entrega, pendientes string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	m.cargos[id-1].EndedAt, m.cargos[id-1].Entrega, m.cargos[id-1].Pendientes = &now, entrega, pendientes
	return nil
}

func (m *memory) Cargo(_ context.Context, id int64) (store.Cargo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cargos[id-1], nil
}

func (m *memory) ListCargos(_ context.Context, limit int) ([]store.Cargo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := append([]store.Cargo(nil), m.cargos...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

func (m *memory) AddRuta(_ context.Context, e store.RutaEntry) (store.RutaEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e.ID, e.CreatedAt, e.UpdatedAt = int64(len(m.entries)+1), time.Now(), time.Now()
	m.entries = append(m.entries, e)
	return e, nil
}

func (m *memory) Ruta(_ context.Context, id int64) (store.RutaEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id < 1 || int(id) > len(m.entries) {
		return store.RutaEntry{}, store.ErrNotFound
	}
	return m.entries[id-1], nil
}

func (m *memory) UpdateRuta(_ context.Context, e store.RutaEntry, _ string) (store.RutaEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[e.ID-1] = e
	return e, nil
}

func (m *memory) SearchRuta(_ context.Context, kind, text string, limit int) ([]store.RutaEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []store.RutaEntry{}
	for i := len(m.entries) - 1; i >= 0 && len(out) < limit; i-- {
		e := m.entries[i]
		if (kind == "" || e.Kind == kind) && (text == "" || strings.Contains(strings.ToLower(e.Title+e.Body), strings.ToLower(text))) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (m *memory) RecordAgentAction(_ context.Context, a store.AgentAction) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.actions = append(m.actions, a)
	return nil
}

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func connect(t *testing.T, url, token string) (*mcp.ClientSession, error) {
	t.Helper()
	c := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
	return c.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: url, HTTPClient: &http.Client{Transport: bearer{token}}}, nil)
}

func call(t *testing.T, s *mcp.ClientSession, tool string, args map[string]any) (map[string]any, string) {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	if res.IsError {
		var msg []string
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				msg = append(msg, tc.Text)
			}
		}
		return nil, strings.Join(msg, " ")
	}
	b, _ := json.Marshal(res.StructuredContent)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out, ""
}

func setup(t *testing.T) (*memory, string, map[string]string) {
	m := &memory{keys: map[string]store.AgentKey{}}
	tokens := map[string]string{}
	add := func(name string, id int64, write bool, expires time.Time, revoked bool) {
		tok, h, err := NewToken()
		if err != nil {
			t.Fatal(err)
		}
		k := store.AgentKey{ID: id, Name: name, CanWrite: write, ExpiresAt: expires}
		if revoked {
			now := time.Now()
			k.RevokedAt = &now
		}
		m.keys[h] = k
		tokens[name] = tok
	}
	add("escritor", 1, true, time.Now().Add(time.Hour), false)
	add("lector", 2, false, time.Now().Add(time.Hour), false)
	add("vencida", 3, true, time.Now().Add(-time.Hour), false)
	add("revocada", 4, true, time.Now().Add(time.Hour), true)
	srv := httptest.NewServer((&Server{Store: m}).Handler())
	t.Cleanup(srv.Close)
	return m, srv.URL, tokens
}

func TestRutaAuth(t *testing.T) {
	_, url, tokens := setup(t)
	for _, tok := range []string{"", "rnd_nope", tokens["vencida"], tokens["revocada"]} {
		req, _ := http.NewRequest("POST", url, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("token %q: status %d, want 401", tok, res.StatusCode)
		}
	}

	// A read-only key sees only the reading tools.
	s, err := connect(t, url, tokens["lector"])
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	tools, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range tools.Tools {
		names = append(names, tl.Name)
	}
	if strings.Join(names, ",") != "ruta_buscar,ruta_inicio,ruta_leer" {
		t.Errorf("read-only tools = %v", names)
	}
	if !strings.Contains(s.InitializeResult().Instructions, "ruta_inicio") {
		t.Errorf("instructions missing: %q", s.InitializeResult().Instructions)
	}
}

func TestRutaCargoFlow(t *testing.T) {
	m, url, tokens := setup(t)
	m.entries = append(m.entries, store.RutaEntry{ID: 1, Kind: store.KindVivido, Title: "La escuela", Body: "uniformes y recreos", Author: "p0dxD", Tags: []string{}})
	s, err := connect(t, url, tokens["escritor"])
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, msg := call(t, s, "ruta_anotar", map[string]any{"tipo": "nota", "titulo": "x", "cuerpo": "y"}); !strings.Contains(msg, "cargo_tomar") {
		t.Errorf("writing without a cargo: %q", msg)
	}
	out, msg := call(t, s, "cargo_tomar", map[string]any{"proposito": "probar la Ruta"})
	if msg != "" || out["cargo"].(map[string]any)["proposito"] != "probar la Ruta" {
		t.Fatalf("cargo_tomar: %v %s", out, msg)
	}
	if _, msg := call(t, s, "cargo_tomar", map[string]any{"proposito": "otro"}); !strings.Contains(msg, "ya tiene abierto") {
		t.Errorf("second cargo: %q", msg)
	}

	out, msg = call(t, s, "ruta_anotar", map[string]any{"tipo": "decision", "titulo": "Respaldos en Garage", "cuerpo": "Porque ya está en el clúster.", "etiquetas": []string{" Respaldos "}})
	if msg != "" || out["autor"] != "escritor" || out["por_agente"] != true {
		t.Fatalf("anotar: %v %s", out, msg)
	}
	if m.entries[1].CargoID == nil || *m.entries[1].CargoID != 1 || m.entries[1].Tags[0] != "respaldos" {
		t.Errorf("entry not tied to the cargo, or tags not cleaned: %+v", m.entries[1])
	}

	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"tipo": "vivido", "titulo": "x", "cuerpo": "y"}, "solo lo escriben las personas"},
		{map[string]any{"tipo": "nota", "titulo": "token", "cuerpo": "ghp_abcdefghijklmnopqrstuvwxyz0123456789"}, "secreto"},
		{map[string]any{"tipo": "nota", "titulo": "db", "cuerpo": "password: hunter2hunter2hunter2X9"}, "secreto"},
		{map[string]any{"tipo": "nota", "titulo": "x", "cuerpo": "-----BEGIN OPENSSH PRIVATE KEY-----"}, "secreto"},
		{map[string]any{"tipo": "otra", "titulo": "x", "cuerpo": "y"}, "tipo desconocido"},
	} {
		if _, msg := call(t, s, "ruta_anotar", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("anotar %v: %q, want %q", tc.args, msg, tc.want)
		}
	}
	// Describing where a secret lives is fine.
	if _, msg := call(t, s, "ruta_anotar", map[string]any{"tipo": "manual", "titulo": "DNS", "cuerpo": "token: el que está en el Secret rendimiento-dns"}); msg != "" {
		t.Errorf("a description of a secret was refused: %s", msg)
	}

	if _, msg := call(t, s, "ruta_actualizar", map[string]any{"id": 1, "cuerpo": "otra cosa"}); !strings.Contains(msg, "lo vivido no se cambia") {
		t.Errorf("updating lo vivido: %q", msg)
	}
	out, msg = call(t, s, "ruta_leer", map[string]any{"id": 1})
	if msg != "" || out["cuerpo"] != "uniformes y recreos" {
		t.Errorf("leer vivido: %v %s", out, msg)
	}

	if _, msg := call(t, s, "cargo_entregar", map[string]any{"entrega": ""}); !strings.Contains(msg, "vacía") {
		t.Errorf("empty entrega: %q", msg)
	}
	if _, msg := call(t, s, "cargo_entregar", map[string]any{"entrega": "Anoté la decisión de respaldos.", "pendientes": "copiar a R2"}); msg != "" {
		t.Fatalf("entregar: %s", msg)
	}
	out, msg = call(t, s, "ruta_inicio", nil)
	if msg != "" || out["cargo_abierto"] != nil {
		t.Fatalf("inicio after handover: %v %s", out, msg)
	}
	last := out["ultimas_entregas"].([]any)[0].(map[string]any)
	if last["entrega"] != "Anoté la decisión de respaldos." || last["pendientes"] != "copiar a R2" {
		t.Errorf("last handover = %v", last)
	}

	// Every call was recorded, the refused ones too.
	var refused int
	for _, a := range m.actions {
		if !a.OK {
			refused++
		}
	}
	if len(m.actions) < 12 || refused < 7 {
		t.Errorf("audit log: %d actions, %d refused", len(m.actions), refused)
	}
}

func TestLooksSecret(t *testing.T) {
	for text, want := range map[string]bool{
		"AKIAABCDEFGHIJKLMNOP":         true,
		"api_key=Zx81kq0Pz8vLm2Qw7rT4": true,
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U": true,
		"la contraseña está en el Secret postgres-credentials":                                         false,
		"secret: rendimiento-logs-bucket-key":                                                          false,
		"Garage escucha en el puerto 3900":                                                             false,
	} {
		if got := LooksSecret(text); got != want {
			t.Errorf("LooksSecret(%q) = %v, want %v", text, got, want)
		}
	}
}
