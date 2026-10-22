package ruta

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/p0dxD/rendimiento.ai/internal/mercado"
	"github.com/p0dxD/rendimiento.ai/internal/store"
)

type fakeMercado struct {
	paginas map[string]mercado.Pagina
	saved   []mercado.Pagina
	opened  []string // login + " " + domain
}

func (f *fakeMercado) Puestos(context.Context) ([]mercado.PuestoBrief, error) {
	out := []mercado.PuestoBrief{}
	for name, p := range f.paginas {
		out = append(out, mercado.PuestoBrief{Puesto: name, Nombre: p.Nombre, Tipo: p.Tipo, Dueno: "p0dxD"})
	}
	return out, nil
}
func (f *fakeMercado) LeerPuesto(_ context.Context, name string) (*mercado.Pagina, error) {
	p, ok := f.paginas[name]
	if !ok {
		return nil, errors.New("there is no stall named " + name)
	}
	return &p, nil
}
func (f *fakeMercado) GuardarPuesto(_ context.Context, name string, p mercado.Pagina) error {
	f.paginas[name] = p
	f.saved = append(f.saved, p)
	return nil
}
func (f *fakeMercado) Abrir(_ context.Context, login string, req mercado.Abrir) (*store.App, error) {
	f.opened = append(f.opened, login+" "+req.Dominio)
	name, _, _ := strings.Cut(req.Dominio, ".")
	return &store.App{Name: name, Repo: "rendimiento-paginas/" + name}, nil
}

func TestMercadoTools(t *testing.T) {
	m := &memory{keys: map[string]store.AgentKey{}}
	tokens := map[string]string{}
	for i, name := range []string{"escritor", "lector"} {
		tok, h, err := NewToken()
		if err != nil {
			t.Fatal(err)
		}
		m.keys[h] = store.AgentKey{ID: int64(i + 1), Name: name, Role: map[bool]string{true: store.RoleCollaborator, false: store.RoleReader}[name == "escritor"], CreatedBy: "p0dxD", ExpiresAt: time.Now().Add(time.Hour)}
		tokens[name] = tok
	}
	fm := &fakeMercado{paginas: map[string]mercado.Pagina{
		"panaderia": {Tipo: "negocio", Idioma: "es", Nombre: "Panadería Ana", Paleta: "cempasuchil", Foto: "fotos/portada.jpg",
			Detalles: []mercado.Detalle{{Etiqueta: "Horario", Texto: "7 a 14"}}},
	}}
	srv := httptest.NewServer((&Server{Store: m, Mercado: fm}).Handler())
	defer srv.Close()

	tools := func(tok string) string {
		s, err := connect(t, srv.URL, tok)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		res, err := s.ListTools(context.Background(), &mcp.ListToolsParams{})
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, tl := range res.Tools {
			if strings.HasPrefix(tl.Name, "mercado_") {
				names = append(names, tl.Name)
			}
		}
		return strings.Join(names, ",")
	}
	if got := tools(tokens["lector"]); got != "mercado_leer,mercado_puestos" {
		t.Fatalf("read-only key's Mercado tools = %s", got)
	}
	if got := tools(tokens["escritor"]); got != "mercado_abrir,mercado_editar,mercado_leer,mercado_puestos" {
		t.Fatalf("writer's Mercado tools = %s", got)
	}

	s, err := connect(t, srv.URL, tokens["escritor"])
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	out, msg := call(t, s, "mercado_puestos", map[string]any{})
	if msg != "" || len(out["puestos"].([]any)) != 1 {
		t.Fatalf("puestos = %v %s", out, msg)
	}
	if _, msg := call(t, s, "mercado_editar", map[string]any{"puesto": "panaderia", "lema": "Pan de pueblo"}); !strings.Contains(msg, "cargo") {
		t.Fatalf("editing without a cargo: %q", msg)
	}
	call(t, s, "cargo_tomar", map[string]any{"proposito": "Cambiar el lema de la panadería, como pidió p0dxD"})
	if _, msg := call(t, s, "mercado_editar", map[string]any{"puesto": "panaderia"}); !strings.Contains(msg, "ningún cambio") {
		t.Fatalf("an edit without changes: %q", msg)
	}
	out, msg = call(t, s, "mercado_editar", map[string]any{"puesto": "panaderia", "lema": "Pan de pueblo", "paleta": "grana"})
	if msg != "" {
		t.Fatal(msg)
	}
	p := fm.paginas["panaderia"]
	if p.Lema != "Pan de pueblo" || p.Paleta != "grana" || p.Nombre != "Panadería Ana" || p.Foto != "fotos/portada.jpg" || len(p.Detalles) != 1 {
		t.Fatalf("only the fields sent change, photos stay: %+v", p)
	}
	if _, msg := call(t, s, "mercado_editar", map[string]any{"puesto": "nadie", "lema": "x"}); !strings.Contains(msg, "no stall") {
		t.Fatalf("an unknown stall: %q", msg)
	}

	pagina := map[string]any{"tipo": "negocio", "idioma": "es", "nombre": "Tacos Beto", "paleta": "nopal"}
	if _, msg := call(t, s, "mercado_abrir", map[string]any{"direccion": "tacos.example.com", "pagina": pagina}); !strings.Contains(msg, "pedido_por_la_persona") {
		t.Fatalf("opening without the person's request: %q", msg)
	}
	out, msg = call(t, s, "mercado_abrir", map[string]any{"direccion": "tacos.example.com", "pagina": pagina, "pedido_por_la_persona": true})
	if msg != "" || out["puesto"] != "tacos" || len(fm.opened) != 1 || fm.opened[0] != "p0dxD tacos.example.com" {
		t.Fatalf("open = %v %q %v", out, msg, fm.opened)
	}
	// Every call is audited.
	var mercadoCalls int
	for _, a := range m.actions {
		if strings.HasPrefix(a.Tool, "mercado_") {
			mercadoCalls++
		}
	}
	if mercadoCalls < 6 {
		t.Fatalf("audited mercado calls = %d", mercadoCalls)
	}
}
