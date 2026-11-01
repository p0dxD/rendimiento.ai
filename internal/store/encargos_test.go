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

// The estimator's answer keeps the route it implies, unless a person chose
// one already; an estimated encargo is not listed to estimate again, and
// only a pending one can be routed.
func TestEncargoEstimate(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	a, _ := s.CreateEncargo(ctx, Encargo{Title: "estimar-a", CreatedBy: "p0dxD"})
	b, _ := s.CreateEncargo(ctx, Encargo{Title: "estimar-b", CreatedBy: "p0dxD"})
	waiting := func(id int64) bool {
		list, err := s.EncargosToEstimate(ctx, 200)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range list {
			if e.ID == id {
				return true
			}
		}
		return false
	}
	if !waiting(a.ID) || !waiting(b.ID) {
		t.Fatal("new encargos not listed to estimate")
	}
	if _, err := s.RouteEncargo(ctx, b.ID, "p0dxD", RouteLocal); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveEstimate(ctx, a.ID, 2, 440_000, "un texto", RoutePlatform); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveEstimate(ctx, b.ID, 1, 110_000, "otro", RoutePlatform); err != nil {
		t.Fatal(err)
	}
	if waiting(a.ID) || waiting(b.ID) {
		t.Fatal("estimated encargos still listed to estimate")
	}
	a, _ = s.Encargo(ctx, a.ID)
	if a.Difficulty == nil || *a.Difficulty != 2 || *a.EstimateMicros != 440_000 || a.EstimateReason != "un texto" || a.Route != RoutePlatform {
		t.Fatalf("estimated %+v", a)
	}
	if b, _ = s.Encargo(ctx, b.ID); b.Route != RouteLocal || b.Log[len(b.Log)-1].What != "routed" {
		t.Fatalf("a person's route was replaced: %+v", b)
	}
	if a, err := s.ReestimateEncargo(ctx, a.ID, "p0dxD"); err != nil || a.Difficulty != nil || a.Route != "" || !waiting(a.ID) {
		t.Fatalf("estimate again: %+v (%v)", a, err)
	}
	if b, err := s.ReestimateEncargo(ctx, b.ID, "p0dxD"); err != nil || b.Route != RouteLocal {
		t.Fatalf("estimating again dropped a person's route: %+v (%v)", b, err)
	}
	if _, err := s.CancelEncargo(ctx, a.ID, "p0dxD", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RouteEncargo(ctx, a.ID, "p0dxD", RouteLocal); !errors.Is(err, ErrEncargoState) {
		t.Fatalf("routed a cancelled encargo: %v", err)
	}
	if waiting(a.ID) {
		t.Fatal("a dropped encargo is listed to estimate")
	}
}

// The coding agent gets the encargos a person sent it first, then (in auto)
// the easy ones; giving one back leaves it for the person's own agent.
func TestEncargosForTheCoder(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	org, err := s.CreateOrg(ctx, "codigo-org", "Código", "p0dxD")
	if err != nil {
		t.Fatal(err)
	}
	easy, _ := s.CreateEncargo(ctx, Encargo{Title: "codigo-facil", App: "tienda", CreatedBy: "p0dxD", OrgID: org.ID})
	hard, _ := s.CreateEncargo(ctx, Encargo{Title: "codigo-dificil", App: "tienda", CreatedBy: "p0dxD", OrgID: org.ID})
	noApp, _ := s.CreateEncargo(ctx, Encargo{Title: "codigo-sin-app", CreatedBy: "p0dxD", OrgID: org.ID})
	_ = s.SaveEstimate(ctx, easy.ID, 1, 110_000, "", RoutePlatform)
	_ = s.SaveEstimate(ctx, hard.ID, 8, 7_000_000, "", RouteLocal)
	next := func() int64 {
		for {
			e, err := s.EncargoForCoder(ctx)
			if errors.Is(err, ErrNotFound) {
				return 0
			}
			if err != nil {
				t.Fatal(err)
			}
			if e.OrgID == org.ID {
				return e.ID
			}
			// another test's encargo: not this organization's business
			t.Fatalf("an encargo of org %d, from another test", e.OrgID)
		}
	}
	if got := next(); got != 0 {
		t.Fatalf("the coding agent is off, yet got %d", got)
	}
	if err := s.SetCodeAgent(ctx, org.ID, CodeAgentAsked, "p0dxD"); err != nil {
		t.Fatal(err)
	}
	if got := next(); got != 0 {
		t.Fatalf("asked, with nothing sent, got %d", got)
	}
	if n, err := s.DoEncargos(ctx, org.ID, []int64{hard.ID, noApp.ID, 999999}, "p0dxD"); err != nil || n != 1 {
		t.Fatalf("sent %d (%v), want only the one with an app", n, err)
	}
	if got := next(); got != hard.ID {
		t.Fatalf("asked: got %d, want the one sent %d", got, hard.ID)
	}
	_ = s.SetCodeAgent(ctx, org.ID, CodeAgentAuto, "p0dxD")
	key, err := s.SystemKey(ctx, org.ID, "agente de código")
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := s.SystemKey(ctx, org.ID, "agente de código"); again.ID != key.ID {
		t.Fatalf("a second key %d for the same agent (%d)", again.ID, key.ID)
	}
	cg, _ := s.TakeCargo(ctx, key.ID, "codigo")
	if _, err := s.TakeEncargo(ctx, hard.ID, key.ID, cg.ID, "agente de código"); err != nil {
		t.Fatal(err)
	}
	if got := next(); got != easy.ID {
		t.Fatalf("auto: got %d, want the easy one %d", got, easy.ID)
	}
	if list, _ := s.EncargosOfSystemKey(ctx, "agente de código"); len(list) == 0 {
		t.Fatal("the agent's encargo is not listed")
	}
	_ = s.AddEncargoSpent(ctx, hard.ID, 1_000)
	h, err := s.GiveBackEncargo(ctx, hard.ID, key.ID, "agente de código", "no pude")
	if err != nil || h.Status != EncargoPending || h.DoNow || h.Route != RouteLocal || h.SpentMicros != 1_000 {
		t.Fatalf("given back %+v (%v)", h, err)
	}
}
