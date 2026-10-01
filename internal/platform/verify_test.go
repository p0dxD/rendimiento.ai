package platform

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/p0dxD/rendimiento.ai/api/v1alpha1"
	"github.com/p0dxD/rendimiento.ai/internal/spec"
	"github.com/p0dxD/rendimiento.ai/internal/store"
	"github.com/p0dxD/rendimiento.ai/internal/uptime"
)

func stats(results ...bool) *windowStats {
	w := &windowStats{}
	for _, ok := range results {
		r := store.Probe{OK: ok, LatencyMS: 100}
		if !ok {
			r.Error = "connection refused"
		}
		w.add(r)
	}
	return w
}

func repeat(ok bool, n int) []bool {
	out := make([]bool, n)
	for i := range out {
		out[i] = ok
	}
	return out
}

func TestJudge(t *testing.T) {
	web := [2]string{"web", "public"}
	healthy := map[[2]string]store.CheckStats{web: {Total: 60, OK: 60, P95MS: 120}}
	for _, tc := range []struct {
		name     string
		baseline map[[2]string]store.CheckStats
		window   *windowStats
		ok       bool
		reason   string
		warning  string
	}{
		{"all good", healthy, stats(repeat(true, 15)...), true, "", ""},
		{"one blip is not a failure", healthy, stats(append(repeat(true, 14), false)...), true, "", ""},
		{"broken by the release", healthy, stats(append(repeat(true, 10), repeat(false, 5)...)...), false,
			"web (public): 5 of 15 checks failed (connection refused); it was 100% up in the hour before", ""},
		{"a new service that never works", nil, stats(repeat(false, 15)...), false, "it had no checks before (a new service)", ""},
		{"already broken before: reported, not blamed", map[[2]string]store.CheckStats{web: {Total: 60, OK: 30}},
			stats(repeat(false, 15)...), true, "", "already failing before this release"},
	} {
		got := map[[2]string]*windowStats{web: tc.window}
		ok, reason, warnings := judge(tc.baseline, got)
		if ok != tc.ok || !strings.Contains(reason, tc.reason) || (tc.warning != "" && !strings.Contains(strings.Join(warnings, ";"), tc.warning)) {
			t.Errorf("%s: ok=%v reason=%q warnings=%v", tc.name, ok, reason, warnings)
		}
	}

	slow := &windowStats{}
	for i := 0; i < 15; i++ {
		slow.add(store.Probe{OK: true, LatencyMS: 2500})
	}
	ok, _, warnings := judge(healthy, map[[2]string]*windowStats{web: slow})
	if !ok || len(warnings) != 1 || !strings.Contains(warnings[0], "slower: p95 2.5 s, was 120 ms") {
		t.Errorf("slower release: ok=%v warnings=%v (slower is reported, never rolled back)", ok, warnings)
	}
}

// A release that breaks its service is rolled back to the last good one;
// a healthy release passes.
func TestReleaseVerification(t *testing.T) {
	p, fake, _, kube := setup(t)
	ctx := context.Background()
	var broken atomic.Bool
	p.Verify = VerifySettings{
		Window: 200 * time.Millisecond, Every: 20 * time.Millisecond,
		Rollout: func(context.Context, string, int64) (RolloutState, string, error) { return RolloutHealthy, "", nil },
		Check: func(_ context.Context, tg uptime.Target) store.Probe {
			return store.Probe{AppID: tg.AppID, Service: tg.Service, Kind: tg.Kind, OK: !broken.Load(), Error: "HTTP 502", At: time.Now()}
		},
	}
	yaml := "services:\n  - name: web\n    domain: shop.joserod.space\n    health: { path: /health }\n"
	sha := func(c byte) string { return strings.Repeat(string(c), 40) }
	tree := fstest.MapFS{"rendimiento.yaml": {Data: []byte(yaml)}}
	for _, c := range "ab" {
		fake.files[sha(byte(c))] = tree
	}
	fake.files["main"] = tree
	sp, _ := spec.Parse([]byte(yaml))
	if _, err := p.Onboard(ctx, OnboardRequest{Installation: 7, Repo: "p0dxD/shop", DefaultBranch: "main", Name: "shop", Spec: *sp}, false); err != nil {
		t.Fatal(err)
	}
	app, _ := p.Store.GetApp(ctx, "shop")
	push := func(c byte) {
		t.Helper()
		runs, err := p.HandlePush(ctx, PushEvent{Installation: 7, Repo: "p0dxD/shop", Branch: "main", SHA: sha(c)})
		if err != nil || len(runs) != 1 {
			t.Fatalf("push: %v %v", runs, err)
		}
		waitRun(t, p, runs[0].ID)
	}
	verified := func(number int64) *store.Release {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			r, err := p.Store.GetRelease(ctx, app.ID, number)
			if err == nil && r.VerifyStatus != "" && r.VerifyStatus != store.VerifyRunning {
				return r
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("release %d was not verified", number)
		return nil
	}

	// 1. A healthy release passes.
	push('a')
	if r := verified(1); r.VerifyStatus != store.VerifyPassed || !strings.Contains(r.VerifyMessage, "all 2 checks stayed healthy") {
		t.Fatalf("release 1: %s %q", r.VerifyStatus, r.VerifyMessage)
	}
	// 2. A release that breaks the service is rolled back to #1, as #3.
	broken.Store(true)
	push('b')
	r2 := verified(2)
	if r2.VerifyStatus != store.VerifyFailed || !strings.Contains(r2.VerifyMessage, "rolled back to release #1 (as #3)") || !strings.Contains(r2.VerifyMessage, "HTTP 502") {
		t.Fatalf("release 2: %s %q", r2.VerifyStatus, r2.VerifyMessage)
	}
	r3, err := p.Store.GetRelease(ctx, app.ID, 3)
	if err != nil || r3.RollbackOf == nil || *r3.RollbackOf != 1 || r3.VerifyStatus != store.VerifySkipped || !strings.Contains(r3.VerifyMessage, "automatic rollback") {
		t.Fatalf("release 3 = %+v, %v", r3, err)
	}
	var cr v1alpha1.App
	if err := kube.Get(ctx, client.ObjectKey{Name: "shop"}, &cr); err != nil || cr.Spec.Release != 3 || cr.Spec.Images["web"] != r3.Images["web"] {
		t.Fatalf("App object: release %d images %v (want release 3, #1's images)", cr.Spec.Release, cr.Spec.Images)
	}
	r1, _ := p.Store.GetRelease(ctx, app.ID, 1)
	if r3.Images["web"] != r1.Images["web"] {
		t.Fatalf("rollback images %v, want release 1's %v", r3.Images, r1.Images)
	}

	// 3. With verify.rollback: false, a failing release is only reported.
	off := false
	rel := &store.Release{AppID: app.ID, SHA: sha('b'), Images: r2.Images, Spec: spec.Spec{Services: sp.Services, Verify: &spec.Verify{Rollback: &off}}}
	if err := p.Store.CreateRelease(ctx, rel); err != nil {
		t.Fatal(err)
	}
	p.startVerification(ctx, app, rel)
	if r := verified(rel.Number); r.VerifyStatus != store.VerifyFailedKept || !strings.Contains(r.VerifyMessage, "automatic rollback is off") {
		t.Fatalf("rollback off: %s %q", r.VerifyStatus, r.VerifyMessage)
	}
}
