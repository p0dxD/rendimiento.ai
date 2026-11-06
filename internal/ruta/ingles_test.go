package ruta

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rendimiento-ai/rendimiento/internal/mercado"
	"github.com/rendimiento-ai/rendimiento/internal/store"
)

// Every tool, with every optional part on, has its description and its
// arguments' descriptions in English; an agent whose address asks for
// English (or on an English platform) gets them, and the instructions,
// while the tools keep their names and still work.
func TestIngles(t *testing.T) {
	m := &memory{keys: map[string]store.AgentKey{}}
	tok, h, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	m.keys[h] = store.AgentKey{ID: 1, Name: "agent", Role: store.RoleCollaborator, ExpiresAt: time.Now().Add(time.Hour)}
	s := &Server{Store: m, Plataforma: &fakePlataforma{runs: map[int64]*store.Run{}},
		Encargos: &fakeEncargos{keys: map[int64]string{}}, Mercado: &fakeMercado{paginas: map[string]mercado.Pagina{}}}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	if len(s.faltan) > 0 {
		t.Fatalf("no English for:\n%s", strings.Join(s.faltan, "\n"))
	}

	for _, tc := range []struct {
		url, lang string
	}{{srv.URL + "?lang=en", "en"}, {srv.URL, "es"}} {
		cs, err := connect(t, tc.url, tok)
		if err != nil {
			t.Fatal(err)
		}
		tools, err := cs.ListTools(context.Background(), &mcp.ListToolsParams{})
		if err != nil {
			t.Fatal(err)
		}
		if len(tools.Tools) < 26 {
			t.Fatalf("%s: %d tools", tc.lang, len(tools.Tools))
		}
		for _, tool := range tools.Tools {
			if english := tool.Description == toolsEN[tool.Name]; english != (tc.lang == "en") {
				t.Errorf("%s: %s: %q", tc.lang, tool.Name, tool.Description)
			}
		}
		instr := cs.InitializeResult().Instructions
		if (tc.lang == "en") != strings.HasPrefix(instr, "This is rendimiento's Ruta") {
			t.Errorf("%s: instructions %q", tc.lang, instr[:40])
		}
		if out, msg := call(t, cs, "ruta_inicio", map[string]any{}); msg != "" || out == nil {
			t.Errorf("%s: ruta_inicio: %q", tc.lang, msg)
		}
		cs.Close()
	}

	// The platform's language is the default.
	en := httptest.NewServer((&Server{Store: m, Lang: "en"}).Handler())
	defer en.Close()
	cs, err := connect(t, en.URL, tok)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	if !strings.HasPrefix(cs.InitializeResult().Instructions, "This is") {
		t.Error("Lang en: instructions not in English")
	}
}
