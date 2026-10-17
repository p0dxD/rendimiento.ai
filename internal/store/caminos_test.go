package store

import (
	"context"
	"testing"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/pipeline"
	"github.com/p0dxD/rendimiento.ai/internal/spec"
)

func TestRunHistory(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	app := &App{Name: "caminos", Repo: "o/caminos", Spec: spec.Spec{Services: []spec.Service{{Name: "web"}}}}
	if err := s.CreateApp(ctx, app); err != nil {
		t.Fatal(err)
	}
	steps := func() []pipeline.Step {
		return []pipeline.Step{{ID: "web:test", Service: "web", Kind: pipeline.KindTest}, {ID: "web:build", Service: "web", Kind: pipeline.KindBuild}}
	}
	// main: built and released
	main := &Run{AppID: app.ID, SHA: "aaa", Branch: "main", Event: "push", Deploy: true}
	if err := s.CreateRun(ctx, main, steps()); err != nil {
		t.Fatal(err)
	}
	_ = s.FinishRun(ctx, main.ID, RunSucceeded, "released #1")
	if err := s.CreateRelease(ctx, &Release{AppID: app.ID, RunID: &main.ID, SHA: "aaa", Images: map[string]string{"web": "r/w@sha256:1"}}); err != nil {
		t.Fatal(err)
	}
	// a branch whose tests failed
	br := &Run{AppID: app.ID, SHA: "bbb", Branch: "arreglo", Event: "push"}
	if err := s.CreateRun(ctx, br, steps()); err != nil {
		t.Fatal(err)
	}
	_ = s.UpdateStep(ctx, br.ID, "web:test", pipeline.StepResult{Status: pipeline.StatusFailed, Message: "2 tests failed", Finished: time.Now()})
	_ = s.FinishRun(ctx, br.ID, RunFailed, "web:test failed")

	h, err := s.RunHistory(ctx, app.ID, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 2 || h[0].ID != main.ID || h[1].ID != br.ID {
		t.Fatalf("history = %+v", h)
	}
	if h[0].Release != 1 || len(h[0].Failed) != 0 || h[0].Message != "released #1" {
		t.Fatalf("main run = %+v", h[0])
	}
	if h[1].Release != 0 || len(h[1].Failed) != 1 || h[1].Failed[0] != "web:test" || h[1].Status != RunFailed {
		t.Fatalf("branch run = %+v", h[1])
	}
	if old, _ := s.RunHistory(ctx, app.ID, time.Now().Add(time.Hour)); len(old) != 0 {
		t.Fatalf("runs before since were listed: %+v", old)
	}
}
