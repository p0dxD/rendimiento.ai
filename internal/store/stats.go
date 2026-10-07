package store

import (
	"context"
	"time"
)

// DeliveryStats summarize how the platform shipped over a period: the
// public stats page's headline numbers. Durations are in seconds.
type DeliveryStats struct {
	Deploys          int     `json:"deploys"`          // releases from builds (not rollbacks)
	Builds           int     `json:"builds"`           // default-branch runs that finished
	BuildsSucceeded  int     `json:"buildsSucceeded"`  // of those
	MedianBuildSec   float64 `json:"medianBuildSec"`   // started → finished, successful builds
	MedianLeadSec    float64 `json:"medianLeadSec"`    // push (run created) → release
	Verified         int     `json:"verified"`         // releases that passed verification
	FailedVerify     int     `json:"failedVerify"`     // releases that failed it
	AutoRollbacks    int     `json:"autoRollbacks"`    // releases rolled back automatically
	Incidents        int     `json:"incidents"`        // outages that started in the period
	MeanRecoverySec  float64 `json:"meanRecoverySec"`  // of outages that ended
	DeploysPerDay    []Count `json:"deploysPerDay"`    // oldest first, every day of the period
	ActiveApps       int     `json:"activeApps"`       // apps that deployed in the period
	ChangeFailurePct float64 `json:"changeFailurePct"` // failed verifications / verified releases, %
}

// Count is a number for a day.
type Count struct {
	Day string `json:"day"` // YYYY-MM-DD in the requested time zone
	N   int    `json:"n"`
}

// Delivery computes DeliveryStats for the days since `days` ago, with days
// counted in time zone tz (an IANA name).
func (s *Store) Delivery(ctx context.Context, days int, tz string) (*DeliveryStats, error) {
	now := time.Now()
	st, err := s.DeliveryBetween(ctx, now.AddDate(0, 0, -days), now)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT to_char(d, 'YYYY-MM-DD'), COALESCE(n, 0)
		FROM generate_series((now() AT TIME ZONE $2)::date - $1::int + 1, (now() AT TIME ZONE $2)::date, '1 day') AS d
		LEFT JOIN (SELECT (created_at AT TIME ZONE $2)::date AS day, count(*) AS n FROM releases
		           WHERE rollback_of IS NULL AND verify_status <> 'blocked' AND created_at >= now() - make_interval(days => $1::int + 1) GROUP BY 1) r ON r.day = d
		ORDER BY d`, days, tz)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c Count
		if err := rows.Scan(&c.Day, &c.N); err != nil {
			return nil, err
		}
		st.DeploysPerDay = append(st.DeploysPerDay, c)
	}
	return st, rows.Err()
}

// DeliveryBetween computes the summary numbers of DeliveryStats for what
// happened in [from, to); DeploysPerDay is left empty.
func (s *Store) DeliveryBetween(ctx context.Context, from, to time.Time) (*DeliveryStats, error) {
	st := &DeliveryStats{DeploysPerDay: []Count{}}
	err := s.pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM releases WHERE created_at >= $1 AND created_at < $2 AND rollback_of IS NULL AND verify_status <> 'blocked'),
		  (SELECT count(DISTINCT app_id) FROM releases WHERE created_at >= $1 AND created_at < $2 AND rollback_of IS NULL AND verify_status <> 'blocked'),
		  (SELECT count(*) FROM runs WHERE deploy AND created_at >= $1 AND created_at < $2 AND status IN ('succeeded', 'failed')),
		  (SELECT count(*) FROM runs WHERE deploy AND created_at >= $1 AND created_at < $2 AND status = 'succeeded'),
		  (SELECT COALESCE(percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM finished_at - started_at)), 0)
		     FROM runs WHERE deploy AND status = 'succeeded' AND created_at >= $1 AND created_at < $2 AND started_at IS NOT NULL),
		  (SELECT COALESCE(percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM r.created_at - u.created_at)), 0)
		     FROM releases r JOIN runs u ON u.id = r.run_id WHERE r.created_at >= $1 AND r.created_at < $2),
		  (SELECT count(*) FROM releases WHERE created_at >= $1 AND created_at < $2 AND verify_status = 'passed'),
		  (SELECT count(*) FROM releases WHERE created_at >= $1 AND created_at < $2 AND verify_status IN ('failed', 'failed-kept')),
		  (SELECT count(*) FROM releases WHERE created_at >= $1 AND created_at < $2 AND verify_message LIKE 'automatic rollback%'),
		  (SELECT count(*) FROM incidents WHERE started_at >= $1 AND started_at < $2),
		  (SELECT COALESCE(avg(extract(epoch FROM ended_at - started_at)), 0) FROM incidents WHERE started_at >= $1 AND started_at < $2 AND ended_at IS NOT NULL)`,
		from, to).Scan(&st.Deploys, &st.ActiveApps, &st.Builds, &st.BuildsSucceeded, &st.MedianBuildSec, &st.MedianLeadSec,
		&st.Verified, &st.FailedVerify, &st.AutoRollbacks, &st.Incidents, &st.MeanRecoverySec)
	if err != nil {
		return nil, err
	}
	if n := st.Verified + st.FailedVerify; n > 0 {
		st.ChangeFailurePct = 100 * float64(st.FailedVerify) / float64(n)
	}
	return st, nil
}

// DeliveryWeek is one week of DeliveryStats' headline numbers, for trends.
type DeliveryWeek struct {
	Start            string   `json:"start"` // YYYY-MM-DD, the week's first day in the requested time zone
	Deploys          int      `json:"deploys"`
	MedianLeadSec    float64  `json:"medianLeadSec"`              // 0 without deploys
	ChangeFailurePct *float64 `json:"changeFailurePct,omitempty"` // nil when no release was verified
	Incidents        int      `json:"incidents"`
	MeanRecoverySec  *float64 `json:"meanRecoverySec,omitempty"` // nil when no outage ended
}

// DeliveryWeeks returns `weeks` seven-day windows ending at now, oldest first.
func (s *Store) DeliveryWeeks(ctx context.Context, weeks int, now time.Time, tz *time.Location) ([]DeliveryWeek, error) {
	out := make([]DeliveryWeek, 0, weeks)
	for i := weeks - 1; i >= 0; i-- {
		to := now.AddDate(0, 0, -7*i)
		from := to.AddDate(0, 0, -7)
		st, err := s.DeliveryBetween(ctx, from, to)
		if err != nil {
			return nil, err
		}
		w := DeliveryWeek{Start: from.In(tz).Format("2006-01-02"), Deploys: st.Deploys, MedianLeadSec: st.MedianLeadSec, Incidents: st.Incidents}
		if st.Verified+st.FailedVerify > 0 {
			w.ChangeFailurePct = &st.ChangeFailurePct
		}
		if st.Incidents > 0 && st.MeanRecoverySec > 0 {
			w.MeanRecoverySec = &st.MeanRecoverySec
		}
		out = append(out, w)
	}
	return out, nil
}

// SiteStats is the public view of one app's public URL check.
type SiteStats struct {
	App       string     `json:"app"`
	Service   string     `json:"service"`
	Up        *bool      `json:"up,omitempty"` // latest check; nil before the first
	Uptime24h *float64   `json:"uptime24h,omitempty"`
	Uptime30d *float64   `json:"uptime30d,omitempty"`
	P50MS     int        `json:"p50Ms"` // last 24 hours
	Daily     []DayRatio `json:"daily"` // oldest first, one per day of the period
	// Hourly is the last 48 hours, one per hour (Day holds the hour, RFC
	// 3339): a fuller picture while there are few days of history.
	Hourly []DayRatio `json:"hourly"`
}

// DayRatio is a day's share of successful checks (nil: no checks that day).
type DayRatio struct {
	Day    string   `json:"day"`
	Uptime *float64 `json:"uptime,omitempty"`
}

// PublicSites returns every public URL check with its uptime over 24 hours
// and 30 days, its typical response time and its daily uptime for `days`
// days (days counted in time zone tz).
func (s *Store) PublicSites(ctx context.Context, days int, tz string) ([]SiteStats, error) {
	rows, err := s.pool.Query(ctx, `
		WITH checks AS (SELECT DISTINCT app_id, service FROM probe_hourly WHERE kind = 'public' AND hour >= now() - interval '2 days')
		SELECT a.name, c.service,
		  (SELECT ok FROM probes p WHERE p.app_id = c.app_id AND p.service = c.service AND p.kind = 'public' ORDER BY at DESC LIMIT 1),
		  (SELECT sum(ok)::float / NULLIF(sum(total), 0) FROM probe_hourly h WHERE h.app_id = c.app_id AND h.service = c.service AND h.kind = 'public' AND h.hour >= now() - interval '24 hours'),
		  (SELECT sum(ok)::float / NULLIF(sum(total), 0) FROM probe_hourly h WHERE h.app_id = c.app_id AND h.service = c.service AND h.kind = 'public' AND h.hour >= now() - interval '30 days'),
		  (SELECT COALESCE(percentile_disc(0.5) WITHIN GROUP (ORDER BY latency_ms) FILTER (WHERE ok), 0) FROM probes p
		     WHERE p.app_id = c.app_id AND p.service = c.service AND p.kind = 'public' AND p.at >= now() - interval '24 hours')
		FROM checks c JOIN apps a ON a.id = c.app_id ORDER BY a.name, c.service`)
	if err != nil {
		return nil, err
	}
	var out []SiteStats
	for rows.Next() {
		var st SiteStats
		if err := rows.Scan(&st.App, &st.Service, &st.Up, &st.Uptime24h, &st.Uptime30d, &st.P50MS); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, st)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		drows, err := s.pool.Query(ctx, `
			SELECT to_char(d, 'YYYY-MM-DD'), r.ratio
			FROM generate_series((now() AT TIME ZONE $3)::date - $4::int + 1, (now() AT TIME ZONE $3)::date, '1 day') AS d
			LEFT JOIN (SELECT (h.hour AT TIME ZONE $3)::date AS day, sum(h.ok)::float / NULLIF(sum(h.total), 0) AS ratio
			           FROM probe_hourly h JOIN apps a ON a.id = h.app_id
			           WHERE a.name = $1 AND h.service = $2 AND h.kind = 'public' AND h.hour >= now() - make_interval(days => $4::int + 1)
			           GROUP BY 1) r ON r.day = d
			ORDER BY d`, out[i].App, out[i].Service, tz, days)
		if err != nil {
			return nil, err
		}
		for drows.Next() {
			var d DayRatio
			if err := drows.Scan(&d.Day, &d.Uptime); err != nil {
				drows.Close()
				return nil, err
			}
			out[i].Daily = append(out[i].Daily, d)
		}
		drows.Close()
		hrows, err := s.pool.Query(ctx, `
			SELECT to_char(h AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:00:00"Z"'), r.ratio
			FROM generate_series(date_trunc('hour', now()) - interval '47 hours', date_trunc('hour', now()), '1 hour') AS h
			LEFT JOIN (SELECT p.hour, sum(p.ok)::float / NULLIF(sum(p.total), 0) AS ratio
			           FROM probe_hourly p JOIN apps a ON a.id = p.app_id
			           WHERE a.name = $1 AND p.service = $2 AND p.kind = 'public' AND p.hour >= now() - interval '49 hours'
			           GROUP BY 1) r ON r.hour = h
			ORDER BY h`, out[i].App, out[i].Service)
		if err != nil {
			return nil, err
		}
		for hrows.Next() {
			var d DayRatio
			if err := hrows.Scan(&d.Day, &d.Uptime); err != nil {
				hrows.Close()
				return nil, err
			}
			out[i].Hourly = append(out[i].Hourly, d)
		}
		hrows.Close()
	}
	return out, nil
}

// ReleaseEvent is one release, for the public activity feed.
type ReleaseEvent struct {
	App          string    `json:"app"`
	Number       int64     `json:"number"`
	At           time.Time `json:"at"`
	RollbackOf   *int64    `json:"rollbackOf,omitempty"`
	VerifyStatus string    `json:"verifyStatus,omitempty"`
	Automatic    bool      `json:"automatic,omitempty"` // an automatic rollback
}

// RecentReleases lists the newest releases across all apps.
func (s *Store) RecentReleases(ctx context.Context, n int) ([]ReleaseEvent, error) {
	rows, err := s.pool.Query(ctx, `SELECT a.name, r.number, r.created_at, r.rollback_of, r.verify_status, r.verify_message LIKE 'automatic rollback%'
		FROM releases r JOIN apps a ON a.id = r.app_id ORDER BY r.created_at DESC LIMIT $1`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReleaseEvent{}
	for rows.Next() {
		var e ReleaseEvent
		if err := rows.Scan(&e.App, &e.Number, &e.At, &e.RollbackOf, &e.VerifyStatus, &e.Automatic); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ActiveRun is a run waiting or in progress, as the welcome page shows it:
// which app, and since when; nothing about the commit.
type ActiveRun struct {
	App    string    `json:"app"`
	Status string    `json:"status"` // queued or running
	Deploy bool      `json:"deploy"` // false: a branch or pull request check
	Since  time.Time `json:"since"`
}

// ActiveRuns lists the runs queued or running, oldest first.
func (s *Store) ActiveRuns(ctx context.Context, n int) ([]ActiveRun, error) {
	rows, err := s.pool.Query(ctx, `SELECT a.name, r.status, r.deploy, COALESCE(r.started_at, r.created_at)
		FROM runs r JOIN apps a ON a.id = r.app_id WHERE r.status IN ('queued', 'running') ORDER BY r.id LIMIT $1`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ActiveRun{}
	for rows.Next() {
		var r ActiveRun
		if err := rows.Scan(&r.App, &r.Status, &r.Deploy, &r.Since); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
