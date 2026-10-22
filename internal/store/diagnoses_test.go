package store

import (
	"context"
	"testing"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/spec"
)

// Failed runs are diagnosed when the organization has automatic diagnosis
// on; one asked for is, either way; the last good commit is the branch's
// last successful run before.
func TestDiagnoses(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	sp := spec.Spec{Services: []spec.Service{{Name: "web"}}}
	sp.Default()
	app := &App{Name: "shop", Repo: "o/shop", DefaultBranch: "main", Spec: sp}
	if err := s.CreateApp(ctx, app); err != nil {
		t.Fatal(err)
	}
	mk := func(sha, status string) int64 {
		r := &Run{AppID: app.ID, SHA: sha, Branch: "main", Event: "push"}
		if err := s.CreateRun(ctx, r, nil); err != nil {
			t.Fatal(err)
		}
		if err := s.FinishRun(ctx, r.ID, status, ""); err != nil {
			t.Fatal(err)
		}
		return r.ID
	}
	good := mk("good111", RunSucceeded)
	bad := mk("bad2222", RunFailed)
	if sha, err := s.LastGoodSHA(ctx, app.ID, "main", bad); err != nil || sha != "good111" {
		t.Fatalf("last good: %q %v (run %d)", sha, err, good)
	}
	ids, err := s.RunsToDiagnose(ctx, time.Now().Add(-time.Hour), 10)
	if err != nil || len(ids) != 1 || ids[0] != bad {
		t.Fatalf("to diagnose: %v %v", ids, err)
	}
	if err := s.SaveDiagnosis(ctx, Diagnosis{RunID: bad, Status: DiagnosisDone, Text: "falló la prueba", Model: "qwen"}); err != nil {
		t.Fatal(err)
	}
	if ids, _ := s.RunsToDiagnose(ctx, time.Now().Add(-time.Hour), 10); len(ids) != 0 {
		t.Fatalf("diagnosed again: %v", ids)
	}
	if err := s.SetAutoDiagnose(ctx, DefaultOrg, false, "p0dxD"); err != nil {
		t.Fatal(err)
	}
	other := mk("bad3333", RunFailed)
	if ids, _ := s.RunsToDiagnose(ctx, time.Now().Add(-time.Hour), 10); len(ids) != 0 {
		t.Fatalf("with automatic diagnosis off: %v", ids)
	}
	if err := s.AskDiagnosis(ctx, other, "p0dxD"); err != nil {
		t.Fatal(err)
	}
	if ids, _ := s.RunsToDiagnose(ctx, time.Now().Add(-time.Hour), 10); len(ids) != 1 || ids[0] != other {
		t.Fatalf("asked for: %v", ids)
	}
	if d, err := s.GetDiagnosis(ctx, other); err != nil || d.Status != DiagnosisPending || d.AskedBy != "p0dxD" {
		t.Fatalf("pending: %+v %v", d, err)
	}
	if a, _ := s.AISettingsOf(ctx, DefaultOrg); a.AutoDiagnose {
		t.Fatal("automatic diagnosis stays off")
	}
}
