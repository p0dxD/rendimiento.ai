package store

import (
	"context"
	"errors"
	"testing"

	"github.com/p0dxD/rendimiento.ai/internal/spec"
)

func TestPuestos(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	sp := spec.Spec{Services: []spec.Service{{Name: "pagina"}}}
	sp.Default()
	app := &App{Name: "pasteleria-ana", Repo: "paginas/pasteleria-ana", DefaultBranch: "main", Spec: sp}
	other := &App{Name: "shop", Repo: "p0dxD/shop", DefaultBranch: "main", Spec: sp}
	for _, a := range []*App{app, other} {
		if err := s.CreateApp(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CreatePuesto(ctx, &Puesto{AppID: app.ID, Owner: "ana", Tipo: "negocio", Nombre: "Pastelería Ana"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetPuesto(ctx, other.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an app that is not a page: %v", err)
	}
	if err := s.UpdatePuesto(ctx, app.ID, "evento", "La boda"); err != nil {
		t.Fatal(err)
	}
	p, err := s.GetPuesto(ctx, app.ID)
	if err != nil || p.App != "pasteleria-ana" || p.Owner != "ana" || p.Tipo != "evento" || p.Nombre != "La boda" || p.UpdatedAt.Before(p.CreatedAt) {
		t.Fatalf("GetPuesto = %+v %v", p, err)
	}
	list, err := s.ListPuestos(ctx)
	if err != nil || len(list) != 1 || list[0].AppID != app.ID {
		t.Fatalf("ListPuestos = %v %v", list, err)
	}
	// Deleting the app closes its page.
	if err := s.DeleteApp(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.ListPuestos(ctx); len(list) != 0 {
		t.Fatalf("the page outlived its app: %v", list)
	}
}
