package store

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

// Probe is one uptime check of one service: inside the cluster ("internal")
// or through its public URL ("public").
type Probe struct {
	AppID     int64     `json:"-"`
	Service   string    `json:"service"`
	Kind      string    `json:"kind"`
	At        time.Time `json:"at"`
	OK        bool      `json:"ok"`
	Status    int       `json:"status,omitempty"`
	LatencyMS int       `json:"latencyMs"`
	Error     string    `json:"error,omitempty"`
}

// Incident is an outage of one check: from its first failed check until the
// next successful one (EndedAt is nil while it lasts).
type Incident struct {
	ID        int64      `json:"id"`
	AppID     int64      `json:"-"`
	Service   string     `json:"service"`
	Kind      string     `json:"kind"`
	StartedAt time.Time  `json:"startedAt"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
	Error     string     `json:"error,omitempty"`
}

// RecordProbes saves a round of checks.
func (s *Store) RecordProbes(ctx context.Context, probes []Probe) error {
	if len(probes) == 0 {
		return nil
	}
	rows := make([][]any, len(probes))
	for i, p := range probes {
		rows[i] = []any{p.AppID, p.Service, p.Kind, p.At, p.OK, p.Status, p.LatencyMS, p.Error}
	}
	_, err := s.pool.CopyFrom(ctx, pgx.Identifier{"probes"},
		[]string{"app_id", "service", "kind", "at", "ok", "status", "latency_ms", "error"}, pgx.CopyFromRows(rows))
	return err
}

// OpenIncident records the start of an outage and returns its ID.
func (s *Store) OpenIncident(ctx context.Context, in Incident) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO incidents (app_id, service, kind, started_at, error) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		in.AppID, in.Service, in.Kind, in.StartedAt, in.Error).Scan(&id)
	return id, err
}

// CloseIncident records the end of an outage.
func (s *Store) CloseIncident(ctx context.Context, id int64, ended time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE incidents SET ended_at = $2 WHERE id = $1 AND ended_at IS NULL`, id, ended)
	return err
}

// OpenIncidents lists the outages still going on, across all apps: what a
// restarted prober picks up.
func (s *Store) OpenIncidents(ctx context.Context) ([]Incident, error) {
	return s.incidents(ctx, `WHERE ended_at IS NULL`)
}

// Incidents lists an app's outages that overlap [since, now], newest first.
func (s *Store) Incidents(ctx context.Context, appID int64, since time.Time) ([]Incident, error) {
	return s.incidents(ctx, `WHERE app_id = $1 AND (ended_at IS NULL OR ended_at >= $2) ORDER BY started_at DESC LIMIT 200`, appID, since)
}

// incidents runs a query for outages (where is its WHERE clause).
func (s *Store) incidents(ctx context.Context, where string, args ...any) ([]Incident, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, app_id, service, kind, started_at, ended_at, error FROM incidents `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Incident{}
	for rows.Next() {
		var in Incident
		if err := rows.Scan(&in.ID, &in.AppID, &in.Service, &in.Kind, &in.StartedAt, &in.EndedAt, &in.Error); err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// RollupProbes (re)computes the hourly summaries of every hour from since's
// hour on. It is idempotent: the prober runs it every few minutes over the
// last hours, so the current hour's summary stays fresh.
func (s *Store) RollupProbes(ctx context.Context, since time.Time) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO probe_hourly (app_id, service, kind, hour, total, ok, p50_ms, p95_ms)
		SELECT app_id, service, kind, date_trunc('hour', at), count(*), count(*) FILTER (WHERE ok),
		       COALESCE(percentile_disc(0.5) WITHIN GROUP (ORDER BY latency_ms) FILTER (WHERE ok), 0),
		       COALESCE(percentile_disc(0.95) WITHIN GROUP (ORDER BY latency_ms) FILTER (WHERE ok), 0)
		FROM probes WHERE at >= date_trunc('hour', $1::timestamptz)
		GROUP BY 1, 2, 3, 4
		ON CONFLICT (app_id, service, kind, hour) DO UPDATE
		SET total = EXCLUDED.total, ok = EXCLUDED.ok, p50_ms = EXCLUDED.p50_ms, p95_ms = EXCLUDED.p95_ms`, since)
	return err
}

// PruneProbes deletes checks older than 7 days and summaries older than 400
// days (the hourly summaries keep the long-range charts).
func (s *Store) PruneProbes(ctx context.Context, now time.Time) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM probes WHERE at < $1`, now.Add(-7*24*time.Hour)); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM probe_hourly WHERE hour < $1`, now.Add(-400*24*time.Hour))
	return err
}

// UptimeBucket is one point of a chart: the checks in [At, At+bucket).
type UptimeBucket struct {
	At    time.Time `json:"at"`
	Total int       `json:"total"`
	OK    int       `json:"ok"`
	P50MS int       `json:"p50Ms"`
	P95MS int       `json:"p95Ms"`
}

// UptimeSeries is one check's history over a range: totals, response time
// percentiles (of successful checks) and the chart's buckets.
type UptimeSeries struct {
	Service string         `json:"service"`
	Kind    string         `json:"kind"`
	Total   int            `json:"total"`
	OK      int            `json:"ok"`
	P50MS   int            `json:"p50Ms"`
	P95MS   int            `json:"p95Ms"`
	Buckets []UptimeBucket `json:"buckets"`
	Last    *Probe         `json:"last,omitempty"`
}

// Uptime returns every check of an app over [since, now] in buckets of the
// given size. Buckets under an hour are computed from the raw checks (kept
// 7 days); longer ones from the hourly summaries, where a bucket's p50 is
// the average of its hours' and its p95 the highest (an upper bound).
func (s *Store) Uptime(ctx context.Context, appID int64, since time.Time, bucket time.Duration) ([]UptimeSeries, error) {
	interval := fmt.Sprintf("%d seconds", int(bucket.Seconds()))
	var q string
	if bucket < time.Hour {
		q = `SELECT service, kind, date_bin($3::interval, at, 'epoch'::timestamptz) AS b, count(*), count(*) FILTER (WHERE ok),
		        COALESCE(percentile_disc(0.5) WITHIN GROUP (ORDER BY latency_ms) FILTER (WHERE ok), 0),
		        COALESCE(percentile_disc(0.95) WITHIN GROUP (ORDER BY latency_ms) FILTER (WHERE ok), 0)
		     FROM probes WHERE app_id = $1 AND at >= $2 GROUP BY 1, 2, 3 ORDER BY 1, 2, 3`
	} else {
		q = `SELECT service, kind, date_bin($3::interval, hour, 'epoch'::timestamptz) AS b, sum(total)::int, sum(ok)::int,
		        COALESCE(round(avg(p50_ms) FILTER (WHERE ok > 0))::int, 0), COALESCE(max(p95_ms) FILTER (WHERE ok > 0), 0)
		     FROM probe_hourly WHERE app_id = $1 AND hour >= date_trunc('hour', $2::timestamptz) GROUP BY 1, 2, 3 ORDER BY 1, 2, 3`
	}
	rows, err := s.pool.Query(ctx, q, appID, since, interval)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byKey := map[[2]string]*UptimeSeries{}
	for rows.Next() {
		var service, kind string
		var b UptimeBucket
		if err := rows.Scan(&service, &kind, &b.At, &b.Total, &b.OK, &b.P50MS, &b.P95MS); err != nil {
			return nil, err
		}
		key := [2]string{service, kind}
		if byKey[key] == nil {
			byKey[key] = &UptimeSeries{Service: service, Kind: kind}
		}
		sr := byKey[key]
		sr.Buckets = append(sr.Buckets, b)
		sr.Total += b.Total
		sr.OK += b.OK
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Range-wide percentiles: exact from the raw checks while they exist,
	// otherwise from the hourly summaries (median of medians, 95th of p95s).
	pq := `SELECT service, kind,
	          COALESCE(percentile_disc(0.5) WITHIN GROUP (ORDER BY latency_ms) FILTER (WHERE ok), 0),
	          COALESCE(percentile_disc(0.95) WITHIN GROUP (ORDER BY latency_ms) FILTER (WHERE ok), 0)
	       FROM probes WHERE app_id = $1 AND at >= $2 GROUP BY 1, 2`
	if time.Since(since) > 7*24*time.Hour-time.Hour {
		pq = `SELECT service, kind,
		         COALESCE(percentile_disc(0.5) WITHIN GROUP (ORDER BY p50_ms) FILTER (WHERE ok > 0), 0),
		         COALESCE(percentile_disc(0.95) WITHIN GROUP (ORDER BY p95_ms) FILTER (WHERE ok > 0), 0)
		      FROM probe_hourly WHERE app_id = $1 AND hour >= date_trunc('hour', $2::timestamptz) GROUP BY 1, 2`
	}
	prows, err := s.pool.Query(ctx, pq, appID, since)
	if err != nil {
		return nil, err
	}
	for prows.Next() {
		var service, kind string
		var p50, p95 int
		if err := prows.Scan(&service, &kind, &p50, &p95); err != nil {
			prows.Close()
			return nil, err
		}
		if sr := byKey[[2]string{service, kind}]; sr != nil {
			sr.P50MS, sr.P95MS = p50, p95
		}
	}
	prows.Close()
	// The latest check of each, for "up right now".
	lrows, err := s.pool.Query(ctx, `SELECT DISTINCT ON (service, kind) service, kind, at, ok, status, latency_ms, error
		FROM probes WHERE app_id = $1 AND at >= now() - interval '1 hour' ORDER BY service, kind, at DESC`, appID)
	if err != nil {
		return nil, err
	}
	for lrows.Next() {
		p := Probe{AppID: appID}
		if err := lrows.Scan(&p.Service, &p.Kind, &p.At, &p.OK, &p.Status, &p.LatencyMS, &p.Error); err != nil {
			lrows.Close()
			return nil, err
		}
		if sr := byKey[[2]string{p.Service, p.Kind}]; sr != nil {
			sr.Last = &p
		}
	}
	lrows.Close()
	out := make([]UptimeSeries, 0, len(byKey))
	for _, sr := range byKey {
		out = append(out, *sr)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Service != out[j].Service {
			return out[i].Service < out[j].Service
		}
		return out[i].Kind < out[j].Kind
	})
	return out, nil
}

// UptimeSummary is the share of successful checks per app since a time:
// the dashboard's uptime badges.
func (s *Store) UptimeSummary(ctx context.Context, since time.Time) (map[int64]UptimeBucket, error) {
	rows, err := s.pool.Query(ctx, `SELECT app_id, count(*), count(*) FILTER (WHERE ok) FROM probes WHERE at >= $1 GROUP BY 1`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]UptimeBucket{}
	for rows.Next() {
		var id int64
		var b UptimeBucket
		if err := rows.Scan(&id, &b.Total, &b.OK); err != nil {
			return nil, err
		}
		out[id] = b
	}
	return out, rows.Err()
}

// CheckStats summarizes one check's results over a period.
type CheckStats struct {
	Total int
	OK    int
	P95MS int // of successful checks
}

// ProbeStats summarizes an app's checks in [from, to), per service and
// check kind: the baseline a new release is compared with.
func (s *Store) ProbeStats(ctx context.Context, appID int64, from, to time.Time) (map[[2]string]CheckStats, error) {
	rows, err := s.pool.Query(ctx, `SELECT service, kind, count(*), count(*) FILTER (WHERE ok),
		COALESCE(percentile_disc(0.95) WITHIN GROUP (ORDER BY latency_ms) FILTER (WHERE ok), 0)
		FROM probes WHERE app_id = $1 AND at >= $2 AND at < $3 GROUP BY 1, 2`, appID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[[2]string]CheckStats{}
	for rows.Next() {
		var service, kind string
		var st CheckStats
		if err := rows.Scan(&service, &kind, &st.Total, &st.OK, &st.P95MS); err != nil {
			return nil, err
		}
		out[[2]string{service, kind}] = st
	}
	return out, rows.Err()
}
