package mercado

import (
	"context"
	"errors"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// PuestoBrief is a stall as the agents' MCP tools list it.
type PuestoBrief struct {
	Puesto      string    `json:"puesto"` // its app's name: what mercado_leer and mercado_editar take
	Nombre      string    `json:"nombre"`
	Tipo        string    `json:"tipo"`
	Dueno       string    `json:"dueno"` // the GitHub login that opened it
	Direccion   string    `json:"direccion,omitempty"`
	Actualizado time.Time `json:"actualizado"`
}

// Puestos lists the stalls, most recently changed first.
func (m *Mercado) Puestos(ctx context.Context) ([]PuestoBrief, error) {
	ps, err := m.Store.ListPuestos(ctx)
	if err != nil {
		return nil, err
	}
	out := []PuestoBrief{}
	for _, p := range ps {
		b := PuestoBrief{Puesto: p.App, Nombre: p.Nombre, Tipo: p.Tipo, Dueno: p.Owner, Actualizado: p.UpdatedAt}
		if a, err := m.Store.GetAppByID(ctx, p.AppID); err == nil {
			for _, s := range a.Spec.Services {
				if s.Domain != "" {
					b.Direccion = "https://" + s.Domain
					break
				}
			}
		}
		out = append(out, b)
	}
	return out, nil
}

// puesto finds a stall's app by its name; an app that is not a stall is not
// the Mercado's to change.
func (m *Mercado) puesto(ctx context.Context, name string) (*store.App, error) {
	a, err := m.Store.GetApp(ctx, name)
	if err != nil {
		return nil, errors.New(M("there is no stall named %q", name))
	}
	if _, err := m.Store.GetPuesto(ctx, a.ID); err != nil {
		return nil, errors.New(M("%s is an app, not a stall of the Mercado", name))
	}
	return a, nil
}

// LeerPuesto reads a stall's page by the stall's name.
func (m *Mercado) LeerPuesto(ctx context.Context, name string) (*Pagina, error) {
	a, err := m.puesto(ctx, name)
	if err != nil {
		return nil, err
	}
	return m.Leer(ctx, a)
}

// GuardarPuesto saves a stall's changed page by the stall's name (see Guardar).
func (m *Mercado) GuardarPuesto(ctx context.Context, name string, p Pagina) error {
	a, err := m.puesto(ctx, name)
	if err != nil {
		return err
	}
	return m.Guardar(ctx, a, p)
}
