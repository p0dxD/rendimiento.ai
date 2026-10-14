package universe

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/spec"
	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// promReply answers an instant query with one value per client → server pair.
func promReply(w http.ResponseWriter, values map[[2]string]string) {
	var rows []string
	for k, v := range values {
		rows = append(rows, fmt.Sprintf(`{"metric":{"client":%q,"server":%q},"value":[1760000000,%q]}`, k[0], k[1], v))
	}
	fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[%s]}}`, strings.Join(rows, ","))
}

func TestMetricsEdges(t *testing.T) {
	ui, api := [2]string{"wellness/ui", "wellness/api"}, [2]string{"wellness/api", "api.openweathermap.org"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		if !strings.Contains(q, "[1h]") {
			t.Errorf("window not in query: %s", q)
		}
		switch {
		case strings.Contains(q, "request_failed_total"):
			promReply(w, map[[2]string]string{ui: "6"})
		case strings.Contains(q, "request_total"):
			promReply(w, map[[2]string]string{ui: "120", api: "30"})
		// A visitor's browser sends no traces: its side has no duration,
		// and the callee's counts.
		case strings.Contains(q, "histogram_quantile(0.5,") && strings.Contains(q, "client_seconds"):
			promReply(w, map[[2]string]string{ui: "0.0025", api: "NaN"})
		case strings.Contains(q, "histogram_quantile(0.5,") && strings.Contains(q, "server_seconds"):
			promReply(w, map[[2]string]string{ui: "0.012"})
		case strings.Contains(q, "histogram_quantile(0.95,") && strings.Contains(q, "client_seconds"):
			promReply(w, map[[2]string]string{ui: "0.25"})
		case strings.Contains(q, "histogram_quantile(0.95,") && strings.Contains(q, "server_seconds"):
			promReply(w, map[[2]string]string{ui: "0.1"})
		default:
			t.Errorf("unexpected query %s", q)
		}
	}))
	defer srv.Close()

	got, err := (&Metrics{URL: srv.URL}).Edges(context.Background(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if e := got[ui]; e.PerMinute != 2 || math.Abs(e.Errors-0.05) > 1e-9 || e.P50MS != 12 || e.P95MS != 250 {
		t.Fatalf("ui → api = %+v", e)
	}
	if e := got[api]; e.PerMinute != 0.5 || e.P50MS != 0 || e.Errors != 0 {
		t.Fatalf("api → weather = %+v", e)
	}

	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"status":"error","error":"the storage is in read-only mode"}`)
	}))
	defer broken.Close()
	if _, err := (&Metrics{URL: broken.URL}).Edges(context.Background(), time.Hour); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("broken store: %v", err)
	}
}

func TestBuildTraced(t *testing.T) {
	a := &store.App{ID: 1, Name: "wellness", Spec: spec.Spec{Services: []spec.Service{
		{Name: "ui", Needs: []spec.Need{{Kind: spec.NeedService, Service: "api"}}},
		{Name: "api", Needs: []spec.Need{{Kind: spec.NeedPostgres}}},
		{Name: "weather"},
	}}}
	ms := func(p50 float64) EdgeMetrics { return EdgeMetrics{PerMinute: 3, P50MS: p50} }
	u := Build(Input{Apps: []*store.App{a}, Traced: map[[2]string]EdgeMetrics{
		{"wellness/ui", "wellness/api"}:                             ms(10), // declared too
		{"wellness/api", "weather.wellness.svc.cluster.local:8000"}: ms(20), // a cluster address
		{"wellness/api", "postgresql"}:                              ms(2),  // the database, by db.system
		{"wellness/weather", "api.openweathermap.org"}:              ms(90), // outside the cluster
		{"user", "wellness/ui"}:                                     ms(30), // a visitor
		{"wellness/api", "wellness/api"}:                            ms(1),  // a call to itself
		{"someone-else/svc", "wellness/api"}:                        ms(1),  // an unknown caller
	}})
	edges := map[[2]string]Edge{}
	for _, e := range u.Edges {
		edges[[2]string{e.From, e.To}] = e
	}
	check := func(from, to string, p50 float64, sources ...string) {
		t.Helper()
		e, ok := edges[[2]string{from, to}]
		if !ok || e.Metrics == nil || e.Metrics.P50MS != p50 || strings.Join(e.Sources, ",") != strings.Join(sources, ",") {
			t.Fatalf("%s → %s = %+v (metrics %+v)", from, to, e, e.Metrics)
		}
	}
	check("wellness/ui", "wellness/api", 10, SourceDeclared, SourceTraced)
	check("wellness/api", "wellness/weather", 20, SourceTraced)
	check("wellness/api", "wellness/postgres", 2, SourceDeclared, SourceTraced)
	check("wellness/weather", "outside/api.openweathermap.org", 90, SourceTraced)
	check(Internet, "wellness/ui", 30, SourceTraced)
	if len(u.Edges) != 5 {
		t.Fatalf("edges = %+v", u.Edges)
	}
	var outside *System
	for i := range u.Systems {
		if u.Systems[i].ID == Outside {
			outside = &u.Systems[i]
		}
	}
	if outside == nil {
		t.Fatalf("no outside system: %+v", u.Systems)
	}
}
