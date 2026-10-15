package universe

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
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
		if strings.HasSuffix(r.URL.Path, "/query_range") {
			// Trends: calls per minute climb on ui → api; latency is not asked about here.
			start, _ := strconv.ParseInt(r.URL.Query().Get("start"), 10, 64)
			if strings.Contains(q, "request_total") && strings.Contains(q, "[2m30s]") || strings.Contains(q, "request_total") && strings.Contains(q, "[2m]") {
				fmt.Fprintf(w, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"client":"wellness/ui","server":"wellness/api"},"values":[[%d,"1"],[%d,"3"]]}]}}`, start, start+150)
				return
			}
			fmt.Fprint(w, `{"status":"success","data":{"resultType":"matrix","result":[]}}`)
			return
		}
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
	if e := got[ui]; len(e.Trend) != TrendPoints || e.Trend[0] != 1 || e.Trend[1] != 3 || e.Trend[2] != 0 || e.P95Trend != nil {
		t.Fatalf("ui → api trend = %v, p95 %v", e.Trend, e.P95Trend)
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

func TestMetricsSpans(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		if !strings.Contains(q, `service_name="shop/api"`) || !strings.Contains(q, `span_name!~".* http (send|receive)"`) {
			t.Errorf("selector not in query: %s", q)
		}
		row := func(name, kind, v string) string {
			return fmt.Sprintf(`{"metric":{"span_name":%q,"span_kind":%q},"value":[1760000000,%q]}`, name, kind, v)
		}
		reply := func(rows ...string) {
			fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[%s]}}`, strings.Join(rows, ","))
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/query_range"):
			start, _ := strconv.ParseInt(r.URL.Query().Get("start"), 10, 64)
			if strings.Contains(q, "calls_total") {
				fmt.Fprintf(w, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"span_name":"cobrar","span_kind":"SPAN_KIND_INTERNAL"},"values":[[%d,"4"]]}]}}`, start)
				return
			}
			fmt.Fprint(w, `{"status":"success","data":{"resultType":"matrix","result":[]}}`)
		case strings.Contains(q, "STATUS_CODE_ERROR"):
			reply(row("cobrar", "SPAN_KIND_INTERNAL", "6"))
		case strings.Contains(q, "calls_total"):
			reply(row("cobrar", "SPAN_KIND_INTERNAL", "120"), row("GET /pedidos", "SPAN_KIND_SERVER", "600"), row("idle", "SPAN_KIND_INTERNAL", "0"))
		case strings.Contains(q, "histogram_quantile(0.5,"):
			reply(row("cobrar", "SPAN_KIND_INTERNAL", "40"))
		case strings.Contains(q, "histogram_quantile(0.95,"):
			reply(row("cobrar", "SPAN_KIND_INTERNAL", "300"), row("GET /pedidos", "SPAN_KIND_SERVER", "NaN"))
		default:
			t.Errorf("unexpected query %s", q)
		}
	}))
	defer srv.Close()

	got, err := (&Metrics{URL: srv.URL}).Spans(context.Background(), "shop/api", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "GET /pedidos" || got[0].Kind != "server" || got[0].PerMinute != 10 || got[0].P95MS != 0 {
		t.Fatalf("spans = %+v", got)
	}
	c := got[1]
	if c.Name != "cobrar" || c.Kind != "internal" || c.PerMinute != 2 || math.Abs(c.Errors-0.05) > 1e-9 || c.P50MS != 40 || c.P95MS != 300 ||
		len(c.Trend) != TrendPoints || c.Trend[0] != 4 {
		t.Fatalf("cobrar = %+v", c)
	}
}
