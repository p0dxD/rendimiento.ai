package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRuta(t *testing.T) {
	s := open(t)
	ctx := context.Background()

	k, err := s.CreateAgentKey(ctx, "claude", "hash-1", true, "p0dxD", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.AgentKeyByHash(ctx, "hash-1")
	if err != nil || got.ID != k.ID || !got.CanWrite || !got.Usable(time.Now()) {
		t.Fatalf("key by hash: %+v %v", got, err)
	}
	if _, err := s.AgentKeyByHash(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown hash: %v", err)
	}

	c, err := s.TakeCargo(ctx, k.ID, "probar")
	if err != nil || c.KeyName != "claude" {
		t.Fatalf("take: %+v %v", c, err)
	}
	if _, err := s.TakeCargo(ctx, k.ID, "otro"); !errors.Is(err, ErrCargoOpen) {
		t.Fatalf("second open cargo: %v", err)
	}

	e, err := s.AddRuta(ctx, RutaEntry{Kind: KindDecision, Title: "Respaldos", Body: "en Garage", Tags: []string{"respaldos"}, Author: "claude", ByAgent: true, CargoID: &c.ID})
	if err != nil || e.ID == 0 || *e.CargoID != c.ID {
		t.Fatalf("add: %+v %v", e, err)
	}
	if _, err := s.AddRuta(ctx, RutaEntry{Kind: KindVivido, Title: "La escuela", Body: "uniformes y recreos", Author: "p0dxD"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRuta(ctx, RutaEntry{Kind: "otra", Title: "x", Author: "p0dxD"}); err == nil {
		t.Fatal("an unknown kind was stored")
	}

	e.Body = "en Garage, y luego en R2"
	if e, err = s.UpdateRuta(ctx, e, "claude"); err != nil || e.Body != "en Garage, y luego en R2" {
		t.Fatalf("update: %+v %v", e, err)
	}
	var versions int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM ruta_history WHERE entry_id = $1 AND body = 'en Garage'`, e.ID).Scan(&versions); err != nil || versions != 1 {
		t.Fatalf("history: %d %v", versions, err)
	}

	for _, tc := range []struct {
		kind, text string
		want       int
	}{{"", "", 2}, {KindVivido, "", 1}, {"", "RECREOS", 1}, {"", "respaldos", 1}, {"", "nada", 0}} {
		list, err := s.SearchRuta(ctx, tc.kind, tc.text, 50)
		if err != nil || len(list) != tc.want {
			t.Errorf("search %q %q = %d entries (%v), want %d", tc.kind, tc.text, len(list), err, tc.want)
		}
	}
	if err := s.ArchiveRuta(ctx, e.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.SearchRuta(ctx, "", "", 50); len(list) != 1 {
		t.Errorf("archived entries are listed: %d", len(list))
	}

	if err := s.RecordAgentAction(ctx, AgentAction{KeyID: k.ID, CargoID: &c.ID, Tool: "ruta_anotar", Args: "{}", OK: true}); err != nil {
		t.Fatal(err)
	}
	if acts, err := s.ListAgentActions(ctx, 10); err != nil || len(acts) != 1 || acts[0].KeyName != "claude" {
		t.Fatalf("actions: %+v %v", acts, err)
	}

	if err := s.HandOver(ctx, c.ID, "hecho", "R2"); err != nil {
		t.Fatal(err)
	}
	if err := s.HandOver(ctx, c.ID, "otra vez", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("handing over twice: %v", err)
	}
	if _, err := s.TakeCargo(ctx, k.ID, "segundo"); err != nil {
		t.Fatalf("a new cargo after handing over: %v", err)
	}

	// Revoking closes the open cargo, and the key stops being usable.
	if err := s.RevokeAgentKey(ctx, k.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OpenCargo(ctx, k.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cargo still open after revoking: %v", err)
	}
	if got, _ := s.AgentKey(ctx, k.ID); got.Usable(time.Now()) {
		t.Fatal("a revoked key is usable")
	}
	if err := s.RevokeAgentKey(ctx, k.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoking twice: %v", err)
	}
}
