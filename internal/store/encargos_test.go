package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// An encargo goes pending → taken → waiting → taken → delivered → returned
// → taken → delivered → reviewed; only one agent has it, and every step is
// in its history.
func TestEncargos(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	k1, err := s.CreateAgentKey(ctx, DefaultOrg, "uno", "h1", RoleCollaborator, nil, "p0dxD", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	k2, _ := s.CreateAgentKey(ctx, DefaultOrg, "dos", "h2", RoleCollaborator, nil, "p0dxD", time.Now().Add(time.Hour))
	c1, _ := s.TakeCargo(ctx, k1.ID, "arreglar")
	c2, _ := s.TakeCargo(ctx, k2.ID, "también")

	e, err := s.CreateEncargo(ctx, Encargo{Title: "Arreglar analizar-accion", Body: "falla con 500", App: "stockpulse", CreatedBy: "p0dxD"})
	if err != nil || e.Status != EncargoPending || len(e.Links) != 0 {
		t.Fatalf("created %+v (%v)", e, err)
	}
	if e, err = s.TakeEncargo(ctx, e.ID, k1.ID, c1.ID, "uno"); err != nil || e.Status != EncargoTaken || e.KeyName != "uno" {
		t.Fatalf("taken %+v (%v)", e, err)
	}
	if _, err := s.TakeEncargo(ctx, e.ID, k2.ID, c2.ID, "dos"); !errors.Is(err, ErrEncargoState) {
		t.Fatalf("taken twice: %v", err)
	}
	if _, err := s.DeliverEncargo(ctx, e.ID, k2.ID, "dos", "no es mío", nil); !errors.Is(err, ErrEncargoState) {
		t.Fatalf("delivered by another: %v", err)
	}
	if e, err = s.WaitEncargo(ctx, e.ID, k1.ID, 655, "uno"); err != nil || e.Status != EncargoWaiting || *e.WaitingRun != 655 {
		t.Fatalf("waiting %+v (%v)", e, err)
	}
	if e, err = s.ResumeEncargo(ctx, e.ID, k1.ID, "uno", "la ejecución 655 pasó"); err != nil || e.Status != EncargoTaken || e.WaitingRun != nil {
		t.Fatalf("resumed %+v (%v)", e, err)
	}
	if e, err = s.DeliverEncargo(ctx, e.ID, k1.ID, "uno", "Rama arreglo lista", []string{"https://github.com/o/stockpulse/pull/new/arreglo"}); err != nil || e.Status != EncargoDelivered {
		t.Fatalf("delivered %+v (%v)", e, err)
	}
	if e, err = s.ReviewEncargo(ctx, e.ID, "p0dxD", false, "falta la prueba"); err != nil || e.Status != EncargoPending || e.KeyID != nil || e.Comment != "falta la prueba" {
		t.Fatalf("returned %+v (%v)", e, err)
	}
	e, _ = s.TakeEncargo(ctx, e.ID, k2.ID, c2.ID, "dos")
	e, _ = s.DeliverEncargo(ctx, e.ID, k2.ID, "dos", "Con prueba", nil)
	if e, err = s.ReviewEncargo(ctx, e.ID, "p0dxD", true, ""); err != nil || e.Status != EncargoReviewed {
		t.Fatalf("reviewed %+v (%v)", e, err)
	}
	var steps []string
	for _, l := range e.Log {
		steps = append(steps, l.What)
	}
	want := "created taken waiting resumed delivered returned taken delivered reviewed"
	if got := joinWords(steps); got != want {
		t.Fatalf("log = %s", got)
	}
	open, _ := s.ListEncargos(ctx, DefaultOrg, true, 0)
	all, _ := s.ListEncargos(ctx, DefaultOrg, false, 0)
	if len(open) != 0 || len(all) != 1 {
		t.Fatalf("open %d, all %d", len(open), len(all))
	}
	if _, err := s.CancelEncargo(ctx, e.ID, "p0dxD", ""); !errors.Is(err, ErrEncargoState) {
		t.Fatalf("cancelling a reviewed one: %v", err)
	}
	if _, err := s.TakeEncargo(ctx, 999, k1.ID, c1.ID, "uno"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no such encargo: %v", err)
	}
}

func joinWords(w []string) string {
	out := ""
	for i, s := range w {
		if i > 0 {
			out += " "
		}
		out += s
	}
	return out
}
