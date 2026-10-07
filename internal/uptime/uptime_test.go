package uptime

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/spec"
	"github.com/p0dxD/rendimiento.ai/internal/store"
)

func TestTargets(t *testing.T) {
	apps := []*store.App{{ID: 7, Name: "shop", Spec: spec.Spec{Services: []spec.Service{
		{Name: "web", Port: 3000, Replicas: 2, Domain: "shop.example.com", Health: &spec.Health{Path: "/api/health"}},
		{Name: "db", Port: 5432, Replicas: 1, Health: &spec.Health{TCP: true}},
		{Name: "landing", Port: 8080, Replicas: 1, Domain: "example.com"},
		{Name: "paused", Port: 8080, Replicas: 0, Domain: "paused.example.com"},
	}}}}
	var got []string
	for _, tg := range Targets(apps) {
		line := tg.Service + " " + tg.Kind + " " + tg.URL + tg.Addr
		if tg.AnyAnswer {
			line += " (any answer)"
		}
		got = append(got, line)
	}
	want := []string{
		"web internal http://web.shop.svc.cluster.local/api/health",
		"web public https://shop.example.com/api/health",
		"db internal db.shop.svc.cluster.local:5432",
		"landing internal landing.shop.svc.cluster.local:8080", // no health check: is the port open?
		"landing public https://example.com/ (any answer)",     // no health path: "/" may well be a 404
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("targets:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.WriteHeader(200)
		case "/login":
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
		case "/slow":
			time.Sleep(300 * time.Millisecond)
		case "/missing":
			w.WriteHeader(404)
		default:
			w.WriteHeader(503)
		}
	}))
	defer srv.Close()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	open := ln.Addr().String()
	ln.Close()
	ln, _ = net.Listen("tcp", open)
	defer ln.Close()
	closed, _ := net.Listen("tcp", "127.0.0.1:0")
	closedAddr := closed.Addr().String()
	closed.Close()

	c := NewChecker(150 * time.Millisecond)
	for _, tc := range []struct {
		t      Target
		ok     bool
		status int
		err    string
	}{
		{Target{URL: srv.URL + "/ok"}, true, 200, ""},
		{Target{URL: srv.URL + "/login"}, true, 302, ""}, // a redirect is an answer; not followed
		{Target{URL: srv.URL + "/down"}, false, 503, "HTTP 503"},
		{Target{URL: srv.URL + "/down", AnyAnswer: true}, false, 503, "HTTP 503"}, // a 5xx is down either way
		{Target{URL: srv.URL + "/missing"}, false, 404, "HTTP 404"},
		{Target{URL: srv.URL + "/missing", AnyAnswer: true}, true, 404, ""}, // it answered
		{Target{URL: srv.URL + "/slow"}, false, 0, "timed out"},
		{Target{Addr: open}, true, 0, ""},
		{Target{Addr: closedAddr}, false, 0, "connection refused"},
	} {
		p := c.Check(context.Background(), tc.t)
		if p.OK != tc.ok || p.Status != tc.status || p.Error != tc.err {
			t.Errorf("%s%s: ok=%v status=%d err=%q; want %v %d %q", tc.t.URL, tc.t.Addr, p.OK, p.Status, p.Error, tc.ok, tc.status, tc.err)
		}
	}
}

// fakeStore records what the prober writes.
type fakeStore struct {
	mu        sync.Mutex
	apps      []*store.App
	probes    []store.Probe
	incidents map[int64]*store.Incident
	nextID    int64
}

func (f *fakeStore) ListReleasedApps(context.Context) ([]*store.App, error) { return f.apps, nil }
func (f *fakeStore) RecordProbes(_ context.Context, p []store.Probe) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.probes = append(f.probes, p...)
	return nil
}
func (f *fakeStore) OpenIncident(_ context.Context, in store.Incident) (int64, error) {
	f.nextID++
	in.ID = f.nextID
	f.incidents[in.ID] = &in
	return in.ID, nil
}
func (f *fakeStore) CloseIncident(_ context.Context, id int64, at time.Time) error {
	f.incidents[id].EndedAt = &at
	return nil
}
func (f *fakeStore) OpenIncidents(context.Context) ([]store.Incident, error) {
	var out []store.Incident
	for _, in := range f.incidents {
		if in.EndedAt == nil {
			out = append(out, *in)
		}
	}
	return out, nil
}
func (f *fakeStore) RollupProbes(context.Context, time.Time) error { return nil }
func (f *fakeStore) PruneProbes(context.Context, time.Time) error  { return nil }

func TestIncidents(t *testing.T) {
	up := true
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if !up {
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()
	f := &fakeStore{incidents: map[int64]*store.Incident{}}
	p := &Prober{Store: f, Checker: NewChecker(time.Second), Log: slog.New(slog.NewTextHandler(io.Discard, nil)), state: map[string]*checkState{}}
	// Check the test server as if it were a service's public URL.
	target := Target{AppID: 1, App: "shop", Service: "web", Kind: KindPublic, URL: srv.URL + "/"}
	round := func() { p.observe(context.Background(), target, p.Checker.Check(context.Background(), target)) }
	set := func(v bool) { mu.Lock(); up = v; mu.Unlock() }

	round() // up
	set(false)
	round() // one failure: a blip, no incident yet
	if len(f.incidents) != 0 {
		t.Fatal("a single failure opened an incident")
	}
	round() // second failure in a row: an outage, dated from the first failure
	if len(f.incidents) != 1 || f.incidents[1].EndedAt != nil || f.incidents[1].Error != "HTTP 500" {
		t.Fatalf("incidents = %+v", f.incidents)
	}
	round()
	if len(f.incidents) != 1 {
		t.Fatal("an ongoing outage opened a second incident")
	}
	// A restarted prober picks up the open incident and closes it on recovery.
	p2 := &Prober{Store: f, Checker: p.Checker, Log: p.Log, state: map[string]*checkState{}}
	open, _ := f.OpenIncidents(context.Background())
	for _, in := range open {
		p2.state[key(in.AppID, in.Service, in.Kind)] = &checkState{fails: FailuresToOpen, incident: in.ID}
	}
	set(true)
	p2.observe(context.Background(), target, p2.Checker.Check(context.Background(), target))
	if f.incidents[1].EndedAt == nil {
		t.Fatal("recovery did not close the incident")
	}
}

func TestRound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	_, portStr, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	port, _ := strconv.Atoi(portStr)
	f := &fakeStore{incidents: map[int64]*store.Incident{}, apps: []*store.App{{ID: 1, Name: "shop", Spec: spec.Spec{Services: []spec.Service{
		{Name: "web", Replicas: 1, Port: port}, // its internal check is a TCP dial: recorded whatever the result
	}}}}}
	p := &Prober{Store: f, Checker: NewChecker(time.Second), Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Parallel: 2, state: map[string]*checkState{}}
	p.Round(context.Background())
	if len(f.probes) != 1 || f.probes[0].Service != "web" || f.probes[0].Kind != KindInternal || f.probes[0].AppID != 1 {
		t.Fatalf("probes = %+v", f.probes)
	}
}
