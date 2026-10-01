package store

import (
	"context"
	"testing"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/spec"
)

func TestUptime(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	app := &App{Name: "shop", Repo: "o/shop", Spec: spec.Spec{Services: []spec.Service{{Name: "web"}}}}
	if err := s.CreateApp(ctx, app); err != nil {
		t.Fatal(err)
	}
	// Two hours of checks, one a minute: the web service's internal check
	// fails for 5 minutes in the first hour; three checks in four take
	// 100 ms and the fourth 300 ms, so p50 is 100 and p95 is 300.
	now := time.Now().Truncate(time.Minute)
	start := now.Add(-2 * time.Hour)
	var probes []Probe
	for m := 0; m < 120; m++ {
		at := start.Add(time.Duration(m) * time.Minute)
		ok := m < 30 || m >= 35
		lat := 100
		if m%4 == 3 {
			lat = 300
		}
		probes = append(probes, Probe{AppID: app.ID, Service: "web", Kind: "internal", At: at, OK: ok, LatencyMS: lat})
		probes = append(probes, Probe{AppID: app.ID, Service: "web", Kind: "public", At: at, OK: true, Status: 200, LatencyMS: 50})
	}
	if err := s.RecordProbes(ctx, probes); err != nil {
		t.Fatal(err)
	}

	// 24-hour view (raw checks, 15-minute buckets).
	series, err := s.Uptime(ctx, app.ID, now.Add(-24*time.Hour), 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 2 || series[0].Kind != "internal" || series[1].Kind != "public" {
		t.Fatalf("series = %+v", series)
	}
	in := series[0]
	if in.Total != 120 || in.OK != 115 || in.P50MS != 100 || in.P95MS != 300 || in.Last == nil || !in.Last.OK {
		t.Fatalf("internal = total %d ok %d p50 %d p95 %d last %+v", in.Total, in.OK, in.P50MS, in.P95MS, in.Last)
	}
	sum := 0
	for _, b := range in.Buckets {
		sum += b.Total
	}
	if sum != 120 || len(in.Buckets) < 8 || len(in.Buckets) > 9 {
		t.Fatalf("buckets: %d covering %d checks", len(in.Buckets), sum)
	}

	// Hourly summaries, then the 7-day view reads them.
	if err := s.RollupProbes(ctx, start); err != nil {
		t.Fatal(err)
	}
	if err := s.RollupProbes(ctx, start); err != nil { // idempotent
		t.Fatal(err)
	}
	week, err := s.Uptime(ctx, app.ID, now.Add(-7*24*time.Hour), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if week[0].Total != 120 || week[0].OK != 115 || week[0].P95MS != 300 {
		t.Fatalf("7-day internal = %+v", week[0])
	}

	sumUp, err := s.UptimeSummary(ctx, now.Add(-24*time.Hour))
	if err != nil || sumUp[app.ID].Total != 240 || sumUp[app.ID].OK != 235 {
		t.Fatalf("summary = %+v %v", sumUp, err)
	}

	// Incidents: open, listed as ongoing, closed.
	id, err := s.OpenIncident(ctx, Incident{AppID: app.ID, Service: "web", Kind: "internal", StartedAt: start.Add(30 * time.Minute), Error: "HTTP 503"})
	if err != nil {
		t.Fatal(err)
	}
	if open, _ := s.OpenIncidents(ctx); len(open) != 1 || open[0].ID != id {
		t.Fatalf("open = %+v", open)
	}
	if err := s.CloseIncident(ctx, id, start.Add(35*time.Minute)); err != nil {
		t.Fatal(err)
	}
	list, _ := s.Incidents(ctx, app.ID, now.Add(-24*time.Hour))
	if open, _ := s.OpenIncidents(ctx); len(open) != 0 || len(list) != 1 || list[0].EndedAt == nil {
		t.Fatalf("after close: open %+v, list %+v", open, list)
	}

	// Pruning keeps 7 days of checks.
	if err := s.PruneProbes(ctx, now.Add(8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if left, _ := s.UptimeSummary(ctx, now.Add(-30*24*time.Hour)); left[app.ID].Total != 0 {
		t.Fatalf("checks older than 7 days were kept: %+v", left)
	}
}
