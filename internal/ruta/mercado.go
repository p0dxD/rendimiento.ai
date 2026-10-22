package ruta

import (
	"context"
	"errors"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/p0dxD/rendimiento.ai/internal/mercado"
	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// Mercado is what the mercado_* tools need (a *mercado.Mercado); without it
// (PAGES_ORG unset) the tools are left out.
type Mercado interface {
	Puestos(ctx context.Context) ([]mercado.PuestoBrief, error)
	LeerPuesto(ctx context.Context, name string) (*mercado.Pagina, error)
	GuardarPuesto(ctx context.Context, name string, p mercado.Pagina) error
	Abrir(ctx context.Context, login string, req mercado.Abrir) (*store.App, error)
}

// addMercadoTools registers the Mercado's tools: listing and reading stalls
// for every key; changing and opening them for a key that can write, within
// a cargo. Photos are added from the UI: a new page has no photo fields.
func (s *Server) addMercadoTools(srv *mcp.Server, write bool) {
	addTool(s, srv, &mcp.Tool{Name: "mercado_puestos", Description: "Lista los puestos del Mercado (páginas sin código para personas que no programan): nombre, tipo, dueño y dirección."}, s.puestos)
	addTool(s, srv, &mcp.Tool{Name: "mercado_leer", Description: "Lee la página completa de un puesto: sus textos, colores, detalles y enlaces (las fotos aparecen como rutas)."}, s.leerPuesto)
	if write {
		addTool(s, srv, &mcp.Tool{Name: "mercado_editar", Description: "Cambia la página de un puesto y la publica: solo los campos que mande (nombre, lema, sobre, paleta, detalles, enlaces); el resto, fotos incluidas, se queda como está. Necesita un cargo abierto. Hágalo solo cuando la persona lo pidió."}, s.editarPuesto)
		addTool(s, srv, &mcp.Tool{Name: "mercado_abrir", Description: "Abre un puesto nuevo: crea su repositorio público y publica la página en su dirección. Necesita un cargo abierto y que la persona lo haya pedido (pedido_por_la_persona). Las fotos se agregan después desde la interfaz."}, s.abrirPuesto)
	}
}

type puestosOut struct {
	Puestos []mercado.PuestoBrief `json:"puestos"`
}

type puestoIn struct {
	Puesto string `json:"puesto" jsonschema:"el nombre del puesto, como lo da mercado_puestos"`
}

type paginaOut struct {
	Puesto string         `json:"puesto"`
	Pagina mercado.Pagina `json:"pagina"`
}

type editarPuestoIn struct {
	Puesto   string             `json:"puesto" jsonschema:"el nombre del puesto, como lo da mercado_puestos"`
	Nombre   *string            `json:"nombre,omitempty" jsonschema:"el nombre que se ve en la página"`
	Lema     *string            `json:"lema,omitempty" jsonschema:"una frase corta bajo el nombre"`
	Sobre    *string            `json:"sobre,omitempty" jsonschema:"el texto principal; párrafos separados por una línea en blanco"`
	Paleta   *string            `json:"paleta,omitempty" jsonschema:"cempasuchil, grana, anil, nopal o rosa"`
	Detalles *[]mercado.Detalle `json:"detalles,omitempty" jsonschema:"datos con etiqueta (Horario, Dirección…); reemplazan a los de antes"`
	Enlaces  *[]mercado.Enlace  `json:"enlaces,omitempty" jsonschema:"cómo encontrar al dueño (instagram, facebook, tiktok, whatsapp, correo, telefono, web); reemplazan a los de antes"`
}

type abrirPuestoIn struct {
	Direccion          string        `json:"direccion" jsonschema:"la dirección de la página, p. ej. panaderia-ana.example.com, bajo uno de los dominios de la plataforma"`
	Pagina             nuevaPaginaIn `json:"pagina" jsonschema:"la página, sin fotos"`
	PedidoPorLaPersona bool          `json:"pedido_por_la_persona" jsonschema:"true solo si la persona con quien trabaja pidió abrir este puesto"`
}

// nuevaPaginaIn is a new page as an agent writes it: only the kind and the
// name are required; the Mercado fills in the rest (language, palette…).
type nuevaPaginaIn struct {
	Tipo     string            `json:"tipo" jsonschema:"negocio, evento, persona, causa…: qué es la página"`
	Nombre   string            `json:"nombre" jsonschema:"el nombre que se ve en la página"`
	Idioma   string            `json:"idioma,omitempty" jsonschema:"es o en: el idioma de las etiquetas de la página"`
	Lema     string            `json:"lema,omitempty" jsonschema:"una frase corta bajo el nombre"`
	Sobre    string            `json:"sobre,omitempty" jsonschema:"el texto principal; párrafos separados por una línea en blanco"`
	Paleta   string            `json:"paleta,omitempty" jsonschema:"cempasuchil, grana, anil, nopal o rosa"`
	Detalles []mercado.Detalle `json:"detalles,omitempty" jsonschema:"datos con etiqueta (Horario, Dirección…)"`
	Enlaces  []mercado.Enlace  `json:"enlaces,omitempty" jsonschema:"cómo encontrar al dueño (instagram, facebook, tiktok, whatsapp, correo, telefono, web)"`
}

type abrirPuestoOut struct {
	Puesto string `json:"puesto"`
	Repo   string `json:"repo"`
	Nota   string `json:"nota"`
}

func (s *Server) puestos(ctx context.Context, k store.AgentKey, _ empty) (puestosOut, error) {
	if err := mercadoKey(k); err != nil {
		return puestosOut{}, err
	}
	ps, err := s.Mercado.Puestos(ctx)
	return puestosOut{Puestos: ps}, err
}

func (s *Server) leerPuesto(ctx context.Context, k store.AgentKey, in puestoIn) (paginaOut, error) {
	if err := mercadoKey(k); err != nil {
		return paginaOut{}, err
	}
	p, err := s.Mercado.LeerPuesto(ctx, strings.TrimSpace(in.Puesto))
	if err != nil {
		return paginaOut{}, err
	}
	return paginaOut{Puesto: in.Puesto, Pagina: *p}, nil
}

func (s *Server) editarPuesto(ctx context.Context, k store.AgentKey, in editarPuestoIn) (paginaOut, error) {
	if err := mercadoKey(k); err != nil {
		return paginaOut{}, err
	}
	if _, err := s.cargoFor(ctx, k); err != nil {
		return paginaOut{}, err
	}
	name := strings.TrimSpace(in.Puesto)
	p, err := s.Mercado.LeerPuesto(ctx, name)
	if err != nil {
		return paginaOut{}, err
	}
	changed := false
	set := func(dst *string, v *string) {
		if v != nil {
			*dst, changed = *v, true
		}
	}
	set(&p.Nombre, in.Nombre)
	set(&p.Lema, in.Lema)
	set(&p.Sobre, in.Sobre)
	set(&p.Paleta, in.Paleta)
	if in.Detalles != nil {
		p.Detalles, changed = *in.Detalles, true
	}
	if in.Enlaces != nil {
		p.Enlaces, changed = *in.Enlaces, true
	}
	if !changed {
		return paginaOut{}, errors.New("no mandó ningún cambio: nombre, lema, sobre, paleta, detalles o enlaces")
	}
	if err := checkText(p.Nombre, p.Lema+"\n"+p.Sobre, nil); err != nil {
		return paginaOut{}, err
	}
	if err := s.Mercado.GuardarPuesto(ctx, name, *p); err != nil {
		return paginaOut{}, err
	}
	return paginaOut{Puesto: name, Pagina: *p}, nil
}

func (s *Server) abrirPuesto(ctx context.Context, k store.AgentKey, in abrirPuestoIn) (abrirPuestoOut, error) {
	if err := mercadoKey(k); err != nil {
		return abrirPuestoOut{}, err
	}
	if _, err := s.cargoFor(ctx, k); err != nil {
		return abrirPuestoOut{}, err
	}
	if !in.PedidoPorLaPersona {
		return abrirPuestoOut{}, errors.New("abrir un puesto publica una página y crea un repositorio público: hágalo solo si la persona lo pidió (pedido_por_la_persona: true)")
	}
	if k.CreatedBy == "" {
		return abrirPuestoOut{}, errors.New("esta llave no tiene dueño: el puesto necesita a una persona que lo cuide")
	}
	n := in.Pagina
	p := mercado.Pagina{Tipo: n.Tipo, Nombre: n.Nombre, Idioma: n.Idioma, Lema: n.Lema, Sobre: n.Sobre, Paleta: n.Paleta, Detalles: n.Detalles, Enlaces: n.Enlaces}
	if err := checkText(p.Nombre, p.Lema+"\n"+p.Sobre, nil); err != nil {
		return abrirPuestoOut{}, err
	}
	app, err := s.Mercado.Abrir(ctx, k.CreatedBy, mercado.Abrir{Pagina: p, Dominio: in.Direccion})
	if err != nil {
		return abrirPuestoOut{}, err
	}
	return abrirPuestoOut{Puesto: app.Name, Repo: app.Repo,
		Nota: "La página se construye y se publica en unos minutos. Las fotos se agregan desde Mercado → el puesto, en la interfaz."}, nil
}

// mercadoKey refuses the Mercado to keys of other organizations: the
// Mercado's stalls are the platform's own (its organization's).
func mercadoKey(k store.AgentKey) error {
	if k.OrgID != store.DefaultOrg && k.OrgID != 0 {
		return errors.New("el Mercado es de la organización de la plataforma")
	}
	return nil
}
