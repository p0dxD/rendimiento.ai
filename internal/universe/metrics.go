package universe

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// EdgeMetrics are the numbers on one line of the map, measured from traces
// over the chosen window: how often the caller calls, how many calls fail,
// and how long they take.
type EdgeMetrics struct {
	PerMinute float64 `json:"perMinute"`
	// Errors is the share of failed calls, 0 to 1.
	Errors float64 `json:"errors"`
	P50MS  float64 `json:"p50Ms,omitempty"`
	P95MS  float64 `json:"p95Ms,omitempty"`
}

// Metrics reads the service graph the OpenTelemetry collector's
// servicegraph connector writes to a Prometheus-compatible store
// (VictoriaMetrics): one series per caller and callee.
type Metrics struct {
	URL  string // e.g. http://vmsingle.monitoring.svc:8428
	HTTP *http.Client
}

// Windows are the periods the Universe page offers.
var Windows = map[string]time.Duration{"15m": 15 * time.Minute, "1h": time.Hour, "24h": 24 * time.Hour}

// Edges returns the numbers of every caller → callee pair seen in the
// window, keyed by the collector's client and server labels.
func (m *Metrics) Edges(ctx context.Context, window time.Duration) (map[[2]string]EdgeMetrics, error) {
	w := promDuration(window)
	minutes := window.Minutes()
	out := map[[2]string]EdgeMetrics{}
	type edgeQuery struct {
		q   string
		set func(*EdgeMetrics, float64)
	}
	queries := []edgeQuery{
		{`sum by (client, server) (increase(traces_service_graph_request_total[` + w + `]))`, func(e *EdgeMetrics, v float64) { e.PerMinute = v / minutes }},
		{`sum by (client, server) (increase(traces_service_graph_request_failed_total[` + w + `]))`, func(e *EdgeMetrics, v float64) { e.Errors = v }},
	}
	// How long a call takes, as the caller sees it and as the callee does:
	// the larger. A side that sends no traces (a visitor's browser, a
	// database) is recorded with no duration, so the other side's counts.
	for _, side := range []string{"client", "server"} {
		hist := `sum by (client, server, le) (rate(traces_service_graph_request_` + side + `_seconds_bucket[` + w + `]))`
		queries = append(queries,
			edgeQuery{`histogram_quantile(0.5, ` + hist + `)`, func(e *EdgeMetrics, v float64) { e.P50MS = math.Max(e.P50MS, v*1000) }},
			edgeQuery{`histogram_quantile(0.95, ` + hist + `)`, func(e *EdgeMetrics, v float64) { e.P95MS = math.Max(e.P95MS, v*1000) }},
		)
	}
	for _, q := range queries {
		res, err := m.query(ctx, q.q)
		if err != nil {
			return nil, err
		}
		for _, r := range res {
			k := [2]string{r.labels["client"], r.labels["server"]}
			if k[0] == "" || k[1] == "" {
				continue
			}
			e := out[k]
			q.set(&e, r.value)
			out[k] = e
		}
	}
	// Errors was a count; make it a share of the calls.
	for k, e := range out {
		calls := e.PerMinute * minutes
		if calls <= 0 {
			if e.Errors > 0 {
				e.Errors = 1
			}
		} else {
			e.Errors = math.Min(1, e.Errors/calls)
		}
		out[k] = e
	}
	return out, nil
}

// sample is one series of an instant query.
type sample struct {
	labels map[string]string
	value  float64
}

// query runs an instant PromQL query and returns its series, skipping
// values that are not numbers (NaN from an empty histogram).
func (m *Metrics) query(ctx context.Context, q string) ([]sample, error) {
	hc := m.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(m.URL, "/")+"/api/v1/query?query="+url.QueryEscape(q), nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("metrics: %w", err)
	}
	defer resp.Body.Close()
	var body struct {
		Status string `json:"status"`
		Error  string `json:"error"`
		Data   struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
				Value  [2]any            `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("metrics: HTTP %d: %w", resp.StatusCode, err)
	}
	if body.Status != "success" {
		return nil, fmt.Errorf("metrics: %s", body.Error)
	}
	var out []sample
	for _, r := range body.Data.Result {
		s, _ := r.Value[1].(string)
		v, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		out = append(out, sample{labels: r.Metric, value: v})
	}
	return out, nil
}

// promDuration writes a window the way PromQL reads it (15m, 1h, 24h).
func promDuration(d time.Duration) string {
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}
