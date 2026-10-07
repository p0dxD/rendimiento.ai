package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/pipeline"
	"github.com/p0dxD/rendimiento.ai/internal/spec"
)

// Tests need a disposable Postgres, e.g.:
//
//	docker run -d --rm -e POSTGRES_PASSWORD=test -e POSTGRES_DB=rendimiento -p 127.0.0.1:55432:5432 postgres:17-alpine
//	TEST_DATABASE_URL=postgres://postgres:test@127.0.0.1:55432/rendimiento go test ./internal/store
func open(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if _, err := s.pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate must be idempotent: %v", err)
	}
	return s
}

func TestAppsRunsReleases(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	sp := spec.Spec{Services: []spec.Service{{Name: "web", Test: &spec.Test{Image: "node:20", Command: "npm test"}}}}
	sp.Default()
	app := &App{Name: "hello", Repo: "p0dxD/Hello", InstallationID: 42, DefaultBranch: "main", Spec: sp}
	if err := s.CreateApp(ctx, app); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateApp(ctx, &App{Name: "hello", Repo: "x/y", Spec: sp}); !IsUniqueViolation(err) {
		t.Fatalf("want unique violation, got %v", err)
	}
	apps, err := s.AppsForRepo(ctx, "p0dxd/hello")
	if err != nil || len(apps) != 1 || apps[0].Spec.Services[0].Test.Image != "node:20" {
		t.Fatalf("AppsForRepo = %v %v", apps, err)
	}

	if live, err := s.ListReleasedApps(ctx); err != nil || len(live) != 0 {
		t.Fatalf("before its first release the app is not live: %v %v", live, err)
	}

	steps := pipeline.Plan("hello", "reg", sp)
	run := &Run{AppID: app.ID, SHA: "abc123", Branch: "main", Event: "push", Deploy: true}
	if err := s.CreateRun(ctx, run, steps); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimRun(ctx)
	if err != nil || claimed.ID != run.ID || claimed.Status != RunRunning {
		t.Fatalf("claim = %+v %v", claimed, err)
	}
	if _, err := s.ClaimRun(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("queue should be empty, got %v", err)
	}

	now := time.Now()
	if err := s.UpdateStep(ctx, run.ID, "web:build", pipeline.StepResult{Status: pipeline.StatusRunning, Started: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateStep(ctx, run.ID, "web:build", pipeline.StepResult{Status: pipeline.StatusSucceeded, Digest: "sha256:1", Finished: now}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"line 1\n", "line 2\n"} {
		if err := s.AppendLog(ctx, run.ID, "web:build", c); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var build Step
	for _, st := range got.Steps {
		if st.ID == "web:build" {
			build = st
		}
	}
	if build.Status != "succeeded" || build.Digest != "sha256:1" || build.StartedAt == nil || build.FinishedAt == nil || build.DependsOn[0] != "web:test" {
		t.Fatalf("step = %+v", build)
	}
	if log, _, _ := s.StepLog(ctx, run.ID, "web:build"); log != "line 1\nline 2\n" {
		t.Fatalf("log = %q", log)
	}
	if err := s.FinishRun(ctx, run.ID, RunSucceeded, ""); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		r := &Release{AppID: app.ID, RunID: &run.ID, SHA: "abc123", Images: map[string]string{"web": "reg/hello-web@sha256:1"}, Spec: sp}
		if err := s.CreateRelease(ctx, r); err != nil {
			t.Fatal(err)
		}
		if r.Number != int64(i+1) {
			t.Fatalf("release number = %d", r.Number)
		}
	}
	rel, err := s.GetRelease(ctx, app.ID, 1)
	if err != nil || rel.Images["web"] != "reg/hello-web@sha256:1" {
		t.Fatalf("release = %+v %v", rel, err)
	}
	list, _ := s.ListReleases(ctx, app.ID, 10)
	if len(list) != 2 || list[0].Number != 2 {
		t.Fatalf("releases = %+v", list)
	}
	if live, err := s.ListReleasedApps(ctx); err != nil || len(live) != 1 || live[0].Name != "hello" {
		t.Fatalf("released apps = %v %v", live, err)
	}
}

func TestConcurrentClaimsAndReleaseNumbers(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	sp := spec.Spec{Services: []spec.Service{{Name: "web"}}}
	sp.Default()
	app := &App{Name: "busy", Repo: "a/b", Spec: sp}
	if err := s.CreateApp(ctx, app); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if err := s.CreateRun(ctx, &Run{AppID: app.ID, SHA: "s", Branch: "main", Event: "push"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	seen := map[int64]bool{}
	numbers := map[int64]bool{}
	var wg sync.WaitGroup
	for w := 0; w < 5; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				r, err := s.ClaimRun(ctx)
				if errors.Is(err, ErrNotFound) {
					return
				}
				if err != nil {
					t.Error(err)
					return
				}
				rel := &Release{AppID: app.ID, SHA: "s", Images: map[string]string{"web": "x"}, Spec: sp}
				if err := s.CreateRelease(ctx, rel); err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				if seen[r.ID] || numbers[rel.Number] {
					t.Errorf("duplicate run %d or release %d", r.ID, rel.Number)
				}
				seen[r.ID], numbers[rel.Number] = true, true
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) != 20 || len(numbers) != 20 {
		t.Fatalf("claimed %d runs, %d releases", len(seen), len(numbers))
	}
}

func TestSessionsAndLogCap(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if err := s.CreateSession(ctx, "h1", "p0dxD", time.Hour); err != nil {
		t.Fatal(err)
	}
	if login, err := s.SessionLogin(ctx, "h1"); err != nil || login != "p0dxD" {
		t.Fatalf("login = %q %v", login, err)
	}
	if err := s.CreateSession(ctx, "old", "p0dxD", -time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionLogin(ctx, "old"); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired session accepted")
	}
	_ = s.DeleteSession(ctx, "h1")
	if _, err := s.SessionLogin(ctx, "h1"); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted session accepted")
	}

	sp := spec.Spec{Services: []spec.Service{{Name: "web"}}}
	sp.Default()
	app := &App{Name: "logs", Repo: "a/b", Spec: sp}
	_ = s.CreateApp(ctx, app)
	run := &Run{AppID: app.ID, SHA: "s", Branch: "main", Event: "push"}
	_ = s.CreateRun(ctx, run, pipeline.Plan("logs", "reg", sp))
	big := strings.Repeat("x", maxLog)
	_ = s.AppendLog(ctx, run.ID, "web:build", big)
	_ = s.AppendLog(ctx, run.ID, "web:build", "TAIL")
	log, _, _ := s.StepLog(ctx, run.ID, "web:build")
	if len(log) != maxLog || !strings.HasSuffix(log, "TAIL") {
		t.Fatalf("log len %d, suffix ok %v", len(log), strings.HasSuffix(log, "TAIL"))
	}
}

func TestRequeueOrphansRetriesThenGivesUp(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	sp := spec.Spec{Services: []spec.Service{{Name: "web"}}}
	sp.Default()
	app := &App{Name: "crashy", Repo: "a/b", Spec: sp}
	_ = s.CreateApp(ctx, app)
	run := &Run{AppID: app.ID, SHA: "s", Branch: "main", Event: "push", Deploy: true}
	if err := s.CreateRun(ctx, run, pipeline.Plan("crashy", "reg", sp)); err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 3; attempt++ {
		if _, err := s.ClaimRun(ctx); err != nil {
			t.Fatalf("attempt %d: claim: %v", attempt, err)
		}
		_ = s.UpdateStep(ctx, run.ID, "web:build", pipeline.StepResult{Status: pipeline.StatusRunning, Started: time.Now()})
		_ = s.AppendLog(ctx, run.ID, "web:build", "partial output")
		requeued, failed, err := s.RequeueOrphans(ctx) // the process "restarted"
		if err != nil {
			t.Fatal(err)
		}
		got, _ := s.GetRun(ctx, run.ID)
		if attempt <= maxAttempts {
			if requeued != 1 || got.Status != RunQueued || got.Steps[0].Status != "pending" {
				t.Fatalf("attempt %d: requeued=%d run=%+v", attempt, requeued, got)
			}
			if log, _, _ := s.StepLog(ctx, run.ID, "web:build"); log != "" {
				t.Fatalf("stale log kept: %q", log)
			}
		} else {
			if failed != 1 || got.Status != RunFailed || !strings.Contains(got.Message, "gave up") {
				t.Fatalf("final attempt: failed=%d run=%+v", failed, got)
			}
			assertStepsClosed(t, got)
			return
		}
	}
}

// A finished run must never show a pending or running step.
func assertStepsClosed(t *testing.T, r *Run) {
	t.Helper()
	for _, st := range r.Steps {
		if st.Status == "pending" || st.Status == "running" || st.FinishedAt == nil {
			t.Fatalf("run %d (%s) has unfinished step %+v", r.ID, r.Status, st)
		}
	}
}

func TestFinishAndCancelCloseSteps(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	sp := spec.Spec{Services: []spec.Service{{Name: "web", Test: &spec.Test{Image: "x", Command: "y"}}}}
	sp.Default()
	app := &App{Name: "steps", Repo: "a/b", Spec: sp}
	_ = s.CreateApp(ctx, app)

	run := &Run{AppID: app.ID, SHA: "s", Branch: "main", Event: "push"}
	_ = s.CreateRun(ctx, run, pipeline.Plan("steps", "reg", sp))
	_, _ = s.ClaimRun(ctx)
	_ = s.UpdateStep(ctx, run.ID, "web:test", pipeline.StepResult{Status: pipeline.StatusRunning, Started: time.Now()})
	if err := s.FinishRun(ctx, run.ID, RunCancelled, "cancelled"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetRun(ctx, run.ID)
	assertStepsClosed(t, got)
	for _, st := range got.Steps {
		want := map[string]string{"web:test": "failed", "web:build": "skipped"}[st.ID]
		if st.Status != want {
			t.Fatalf("%s = %s, want %s", st.ID, st.Status, want)
		}
	}

	queued := &Run{AppID: app.ID, SHA: "s2", Branch: "main", Event: "push"}
	_ = s.CreateRun(ctx, queued, pipeline.Plan("steps", "reg", sp))
	if err := s.CancelQueued(ctx, app.ID, "main"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetRun(ctx, queued.ID)
	if got.Status != RunCancelled {
		t.Fatalf("status = %s", got.Status)
	}
	assertStepsClosed(t, got)
}

func TestAddons(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if _, err := s.GetAddon(ctx, "renovate"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unset add-on: %v", err)
	}
	if err := s.PutAddon(ctx, "renovate", true, []byte(`{"schedule":"@daily"}`)); err != nil {
		t.Fatal(err)
	}
	a, err := s.GetAddon(ctx, "renovate")
	if err != nil || !a.Enabled || a.LastScheduled != nil {
		t.Fatalf("addon = %+v, %v", a, err)
	}
	// Two workers race for the same slot: only one wins.
	now := time.Now().Truncate(time.Second)
	won1, _ := s.ClaimSchedule(ctx, "renovate", nil, now)
	won2, _ := s.ClaimSchedule(ctx, "renovate", nil, now)
	if !won1 || won2 {
		t.Fatalf("claims = %v %v", won1, won2)
	}
	later := now.Add(time.Hour)
	if won, _ := s.ClaimSchedule(ctx, "renovate", &now, later); !won {
		t.Fatal("the next slot must be claimable from the recorded one")
	}

	sp := spec.Spec{Services: []spec.Service{{Name: "web"}}}
	app := &App{Name: "shop", Repo: "o/shop", InstallationID: 1, DefaultBranch: "main", Spec: sp}
	if err := s.CreateApp(ctx, app); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateApp(ctx, &App{Name: "blog", Repo: "o/blog", InstallationID: 1, DefaultBranch: "main", Spec: sp}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAppAddon(ctx, app.ID, "renovate", true); err != nil {
		t.Fatal(err)
	}
	on, err := s.AppsWithAddon(ctx, "renovate")
	if err != nil || len(on) != 1 || on[0].Name != "shop" || on[0].Repo != "o/shop" {
		t.Fatalf("apps with renovate = %v, %v", on, err)
	}
	_ = s.SetAppAddon(ctx, app.ID, "renovate", false)
	if on, _ := s.AppsWithAddon(ctx, "renovate"); len(on) != 0 {
		t.Fatal("switched off")
	}

	run := &AddonRun{Addon: "renovate", Trigger: "manual", Repos: []string{"o/shop"}}
	if err := s.CreateAddonRun(ctx, run); err != nil || run.Status != "running" {
		t.Fatalf("run = %+v, %v", run, err)
	}
	if n, _ := s.FailRunningAddonRuns(ctx); n != 1 {
		t.Fatal("a running run is closed after a restart")
	}
	if err := s.FinishAddonRun(ctx, run.ID, "succeeded", "1 PR", map[string]json.RawMessage{"o/shop": []byte(`{"result":"done"}`)}, "log text"); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAddonRun(ctx, "renovate", run.ID)
	var res struct{ Result string }
	if err != nil || got.Status != "succeeded" || got.Log != "log text" || json.Unmarshal(got.Results["o/shop"], &res) != nil || res.Result != "done" {
		t.Fatalf("run = %+v, %v", got, err)
	}
	list, _ := s.ListAddonRuns(ctx, "renovate", 10)
	if len(list) != 1 || list[0].Log != "" {
		t.Fatal("run lists leave the log out")
	}
}
