package store

import (
	"context"
	"testing"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/pipeline"
	"github.com/p0dxD/rendimiento.ai/internal/spec"
)

func TestLogArchiveBookkeeping(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	app := &App{Name: "shop", Repo: "o/shop", Spec: spec.Spec{Services: []spec.Service{{Name: "web"}}}}
	_ = s.CreateApp(ctx, app)
	run := &Run{AppID: app.ID, SHA: "a", Branch: "main", Event: "push", Deploy: true}
	if err := s.CreateRun(ctx, run, []pipeline.Step{{ID: "web:build", Service: "web", Kind: pipeline.KindBuild}}); err != nil {
		t.Fatal(err)
	}
	logText := "building… ✓ done 🚀\n" // multi-byte characters: bytes ≠ characters
	_ = s.AppendLog(ctx, run.ID, "web:build", logText)
	if steps, _ := s.UnarchivedSteps(ctx, time.Now(), 10); len(steps) != 0 {
		t.Fatal("a running run's logs must not be archived")
	}
	_, _ = s.ClaimRun(ctx, nil)
	_ = s.FinishRun(ctx, run.ID, RunSucceeded, "")
	steps, err := s.UnarchivedSteps(ctx, time.Now().Add(time.Second), 10)
	if err != nil || len(steps) != 1 || steps[0].App != "shop" || steps[0].Log != logText {
		t.Fatalf("unarchived = %+v, %v", steps, err)
	}
	if ok, err := s.MarkArchived(ctx, run.ID, "web:build", "runs/shop/1/web_build.log.gz", len(logText)-1); ok || err != nil {
		t.Fatalf("a log of a different size must not be marked: %v %v", ok, err)
	}
	if ok, err := s.MarkArchived(ctx, run.ID, "web:build", "runs/shop/1/web_build.log.gz", len(logText)); !ok || err != nil {
		t.Fatalf("mark archived (len counts bytes): %v %v", ok, err)
	}
	log, ref, err := s.StepLog(ctx, run.ID, "web:build")
	if err != nil || log != "" || ref != "runs/shop/1/web_build.log.gz" {
		t.Fatalf("after archiving: log %q ref %q %v", log, ref, err)
	}
	n, bytes, pending, err := s.ArchiveStats(ctx)
	if err != nil || n != 1 || bytes != int64(len(logText)) || pending != 0 {
		t.Fatalf("stats: %d %d %d %v", n, bytes, pending, err)
	}
}
