package api

import (
	"net/url"
	"testing"
	"time"
)

// A Visits request's window: calendar days run midnight to midnight in the
// visits' time zone (today until now), rolling ranges end now.
func TestVisitPeriod(t *testing.T) {
	tz, _ := time.LoadLocation("America/Mexico_City")
	now := time.Date(2026, 10, 26, 15, 30, 0, 0, tz)
	cases := []struct {
		q          string
		name, unit string
		from, to   time.Time
	}{
		{"range=today", "today", "hour", time.Date(2026, 10, 26, 0, 0, 0, 0, tz), now},
		{"range=yesterday", "yesterday", "hour", time.Date(2026, 10, 25, 0, 0, 0, 0, tz), time.Date(2026, 10, 25, 23, 59, 59, 0, tz)},
		{"day=2026-10-20", "day", "hour", time.Date(2026, 10, 20, 0, 0, 0, 0, tz), time.Date(2026, 10, 20, 23, 59, 59, 0, tz)},
		{"day=2026-10-26", "day", "hour", time.Date(2026, 10, 26, 0, 0, 0, 0, tz), now},
		{"", "7d", "day", now.Add(-7 * 24 * time.Hour), now},
		{"range=30d", "30d", "day", now.Add(-30 * 24 * time.Hour), now},
	}
	for _, c := range cases {
		q, _ := url.ParseQuery(c.q)
		name, p, err := visitPeriod(q, tz, now)
		if err != nil || name != c.name || p.Unit != c.unit || !p.From.Equal(c.from) || !p.To.Equal(c.to) {
			t.Errorf("%q: %s %s %v–%v, %v; want %s %s %v–%v", c.q, name, p.Unit, p.From, p.To, err, c.name, c.unit, c.from, c.to)
		}
	}
	for _, bad := range []string{"range=1y", "day=2026-10-27", "day=2025-01-01", "day=yesterday"} {
		q, _ := url.ParseQuery(bad)
		if _, _, err := visitPeriod(q, tz, now); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}
