package store

import (
	"context"
	"testing"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/spec"
)

func TestDeliveryAndPublicStats(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	app := &App{Name: "shop", Repo: "o/shop", DefaultBranch: "main", Spec: spec.Spec{Services: []spec.Service{{Name: "web", Domain: "shop.example.com"}}}}
	if err := s.CreateApp(ctx, app); err != nil {
		t.Fatal(err)
	}
	// Two deploy runs: one succeeds and releases, one fails.
	ok := &Run{AppID: app.ID, SHA: "a", Branch: "main", Event: "push", Deploy: true}
	bad := &Run{AppID: app.ID, SHA: "b", Branch: "main", Event: "push", Deploy: true}
	for _, r := range []*Run{ok, bad} {
		if err := s.CreateRun(ctx, r, nil); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if _, err := s.ClaimRun(ctx); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.FinishRun(ctx, ok.ID, RunSucceeded, "")
	_ = s.FinishRun(ctx, bad.ID, RunFailed, "tests failed")
	rel := &Release{AppID: app.ID, RunID: &ok.ID, SHA: "a", Images: map[string]string{"web": "w@1"}, Spec: app.Spec}
	if err := s.CreateRelease(ctx, rel); err != nil {
		t.Fatal(err)
	}
	_ = s.SetVerification(ctx, rel.ID, VerifyPassed, "fine")
	back := &Release{AppID: app.ID, SHA: "a", Images: rel.Images, Spec: app.Spec, RollbackOf: &rel.Number}
	_ = s.CreateRelease(ctx, back)
	_ = s.SetVerification(ctx, back.ID, VerifySkipped, "automatic rollback: release #1 failed verification")

	d, err := s.Delivery(ctx, 30, "America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	if d.Deploys != 1 || d.ActiveApps != 1 || d.Builds != 2 || d.BuildsSucceeded != 1 || d.Verified != 1 || d.AutoRollbacks != 1 || len(d.DeploysPerDay) != 30 {
		t.Fatalf("delivery = %+v", d)
	}
	if last := d.DeploysPerDay[29]; last.N != 1 || last.Day != time.Now().In(mustTZ(t, "America/New_York")).Format("2006-01-02") {
		t.Fatalf("today = %+v (rollbacks are not deploys; days are New York days)", last)
	}

	// Windows: nothing happened a month ago; the last week holds it all.
	prev, err := s.DeliveryBetween(ctx, time.Now().AddDate(0, 0, -60), time.Now().AddDate(0, 0, -30))
	if err != nil || prev.Deploys != 0 || prev.Builds != 0 {
		t.Fatalf("previous month = %+v, %v", prev, err)
	}
	weeks, err := s.DeliveryWeeks(ctx, 12, time.Now().Add(time.Minute), mustTZ(t, "America/New_York"))
	if err != nil || len(weeks) != 12 {
		t.Fatalf("weeks = %+v, %v", weeks, err)
	}
	last := weeks[11]
	if last.Deploys != 1 || last.ChangeFailurePct == nil || *last.ChangeFailurePct != 0 || last.MeanRecoverySec != nil {
		t.Fatalf("this week = %+v", last)
	}
	if weeks[0].Deploys != 0 || weeks[0].ChangeFailurePct != nil || weeks[0].Start >= last.Start {
		t.Fatalf("oldest week = %+v", weeks[0])
	}

	// Public checks: an hour of them, one failure.
	var probes []Probe
	for m := 0; m < 60; m++ {
		probes = append(probes, Probe{AppID: app.ID, Service: "web", Kind: "public", At: time.Now().Add(-time.Duration(m) * time.Minute), OK: m != 30, LatencyMS: 80})
	}
	_ = s.RecordProbes(ctx, probes)
	_ = s.RollupProbes(ctx, time.Now().Add(-3*time.Hour))
	sites, err := s.PublicSites(ctx, 90, "America/New_York")
	if err != nil || len(sites) != 1 {
		t.Fatalf("sites = %+v, %v", sites, err)
	}
	site := sites[0]
	if site.App != "shop" || site.Up == nil || !*site.Up || site.Uptime24h == nil || *site.Uptime24h < 0.98 || site.P50MS != 80 || len(site.Daily) != 90 || site.Daily[89].Uptime == nil ||
		len(site.Hourly) != 48 || site.Hourly[47].Uptime == nil {
		t.Fatalf("site = %+v", site)
	}

	recent, err := s.RecentReleases(ctx, 5)
	if err != nil || len(recent) != 2 || recent[0].Number != 2 || !recent[0].Automatic || recent[1].VerifyStatus != VerifyPassed {
		t.Fatalf("recent = %+v, %v", recent, err)
	}
}

func mustTZ(t *testing.T, name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}
