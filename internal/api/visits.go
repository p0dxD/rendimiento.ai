package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/analytics"
	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// visitRanges are the Visits tab's rolling ranges and how their charts are
// bucketed; "today", "yesterday" and a chosen day are calendar days, by hour.
var visitRanges = map[string]struct {
	span time.Duration
	unit string
}{
	"24h": {24 * time.Hour, "hour"},
	"7d":  {7 * 24 * time.Hour, "day"},
	"30d": {30 * 24 * time.Hour, "day"},
}

// visitDaysBack is how far back a chosen day may be (Umami keeps more, but
// the tab is about recent visits).
const visitDaysBack = 90

// visitPeriod is the window a Visits request asks for: range=24h|7d|30d|today|yesterday,
// or day=2026-10-20 (a calendar day in the visits' time zone).
func visitPeriod(q url.Values, tz *time.Location, now time.Time) (string, analytics.Period, error) {
	midnight := func(t time.Time) time.Time {
		t = t.In(tz)
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, tz)
	}
	if d := q.Get("day"); d != "" {
		day, err := time.ParseInLocation("2006-01-02", d, tz)
		if err != nil || day.After(now) || now.Sub(day) > visitDaysBack*24*time.Hour {
			return "", analytics.Period{}, errors.New("day must be a date in the last 90 days, like 2026-10-20")
		}
		return "day", analytics.Period{From: day, To: minTime(day.AddDate(0, 0, 1).Add(-time.Second), now), Unit: "hour", TZ: tz}, nil
	}
	name := q.Get("range")
	switch name {
	case "":
		name = "7d"
	case "today":
		return name, analytics.Period{From: midnight(now), To: now, Unit: "hour", TZ: tz}, nil
	case "yesterday":
		today := midnight(now)
		return name, analytics.Period{From: today.AddDate(0, 0, -1), To: today.Add(-time.Second), Unit: "hour", TZ: tz}, nil
	}
	rg, ok := visitRanges[name]
	if !ok {
		return "", analytics.Period{}, errors.New("range must be today, yesterday, 24h, 7d or 30d")
	}
	return name, analytics.Period{From: now.Add(-rg.span), To: now, Unit: rg.unit, TZ: tz}, nil
}

// minTime is the earlier of a and b.
func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// visitDays is the dashboard summary's period.
const visitDays = 7

// VisitsView is an app's Visits tab: one report per Umami website whose
// domain is one of the app's hostnames.
type VisitsView struct {
	Configured bool                `json:"configured"` // rendimiento can read Umami
	Range      string              `json:"range"`      // today, yesterday, 24h, 7d, 30d, or day
	From       time.Time           `json:"from"`
	To         time.Time           `json:"to"`
	Unit       string              `json:"unit"` // hour | day: how the chart is bucketed
	TimeZone   string              `json:"timeZone"`
	Hosts      []string            `json:"hosts"`              // the app's hostnames
	UmamiURL   string              `json:"umamiURL,omitempty"` // Umami's public address, for links
	Reports    []*analytics.Report `json:"reports"`
}

// appHosts are every hostname an app's services answer on.
func appHosts(a *store.App) []string {
	hosts := []string{}
	for _, svc := range a.Spec.Services {
		hosts = append(hosts, svc.Hosts()...)
	}
	return hosts
}

// visitTZ is the time zone visits are counted in (PUBLIC_STATS_TZ, else UTC).
func (s *Server) visitTZ() *time.Location {
	if tz, err := time.LoadLocation(s.PublicTimeZone); err == nil && s.PublicTimeZone != "" {
		return tz
	}
	return time.UTC
}

// umamiError answers a failed Umami call: a refused login is a setup
// problem, anything else that Umami is unreachable.
func umamiError(w http.ResponseWriter, err error) {
	if errors.Is(err, analytics.ErrAuth) {
		httpError(w, http.StatusBadGateway, "Umami refused rendimiento's user name or password (UMAMI_USERNAME, UMAMI_PASSWORD)")
		return
	}
	httpError(w, http.StatusBadGateway, "could not read Umami: "+err.Error())
}

// visits is an app's Visits tab: Umami's numbers for each of its addresses
// today, yesterday, on a chosen day, or over the last 24 hours, 7 or 30
// days, with the latest visitors of that time.
func (s *Server) visits(w http.ResponseWriter, r *http.Request, _ string) {
	a := s.app(w, r)
	if a == nil {
		return
	}
	tz := s.visitTZ()
	name, period, err := visitPeriod(r.URL.Query(), tz, time.Now())
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	view := VisitsView{Configured: s.Analytics != nil, Range: name, From: period.From, To: period.To, Unit: period.Unit, TimeZone: tz.String(),
		Hosts: appHosts(a), UmamiURL: s.AnalyticsURL, Reports: []*analytics.Report{}}
	if s.Analytics == nil {
		writeJSON(w, view)
		return
	}
	sites, err := s.Analytics.Websites(r.Context())
	if err != nil {
		umamiError(w, err)
		return
	}
	for _, site := range analytics.Match(sites, view.Hosts) {
		rep, err := s.Analytics.Report(r.Context(), site, period)
		if err != nil {
			umamiError(w, err)
			return
		}
		view.Reports = append(view.Reports, rep)
	}
	writeJSON(w, view)
}

// visitActivity is what one visitor of an app did, page by page: the
// website must be one of the app's, and the window at most 31 days.
func (s *Server) visitActivity(w http.ResponseWriter, r *http.Request, _ string) {
	a := s.app(w, r)
	if a == nil {
		return
	}
	if s.Analytics == nil {
		httpError(w, http.StatusNotFound, "visits are off: rendimiento has no Umami account")
		return
	}
	ms := func(k string) (time.Time, bool) {
		n, err := strconv.ParseInt(r.URL.Query().Get(k), 10, 64)
		return time.UnixMilli(n), err == nil && n > 0
	}
	from, ok1 := ms("from")
	to, ok2 := ms("to")
	if !ok1 || !ok2 || !to.After(from) || to.Sub(from) > 31*24*time.Hour {
		httpError(w, http.StatusBadRequest, "from and to must be times in milliseconds, at most 31 days apart")
		return
	}
	sites, err := s.Analytics.Websites(r.Context())
	if err != nil {
		umamiError(w, err)
		return
	}
	for _, site := range analytics.Match(sites, appHosts(a)) {
		if site.ID != r.PathValue("site") {
			continue
		}
		steps, err := s.Analytics.Activity(r.Context(), site, r.PathValue("session"), from, to)
		if err != nil {
			umamiError(w, err)
			return
		}
		writeJSON(w, steps)
		return
	}
	httpError(w, http.StatusNotFound, "that website is not one of this app's")
}

// AppVisits is one app's line in the dashboard summary.
type AppVisits struct {
	App      string          `json:"app"`
	Current  analytics.Stats `json:"current"`
	Previous analytics.Stats `json:"previous"`
}

// VisitsSummary is the dashboard's visitors panel: the last 7 days of
// every app that Umami counts, and the 7 before them.
type VisitsSummary struct {
	Configured bool            `json:"configured"`
	Days       int             `json:"days"`
	Current    analytics.Stats `json:"current"`
	Previous   analytics.Stats `json:"previous"`
	Apps       []AppVisits     `json:"apps"` // most visitors first; only apps Umami counts
}

// addStats adds b's numbers to a.
func addStats(a *analytics.Stats, b analytics.Stats) {
	a.Pageviews += b.Pageviews
	a.Visitors += b.Visitors
	a.Visits += b.Visits
	a.Bounces += b.Bounces
	a.TotalTime += b.TotalTime
}

// visitsSummary is the dashboard's visitors: each app's total of the last
// days, next to the days before.
func (s *Server) visitsSummary(w http.ResponseWriter, r *http.Request, _ string) {
	out := VisitsSummary{Configured: s.Analytics != nil, Days: visitDays, Apps: []AppVisits{}}
	if s.Analytics == nil {
		writeJSON(w, out)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	sites, err := s.Analytics.Websites(ctx)
	if err != nil {
		umamiError(w, err)
		return
	}
	apps, err := s.Store.ListAppsInOrg(ctx, orgOf(r))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	to := time.Now()
	from := to.AddDate(0, 0, -visitDays)
	for _, a := range apps {
		matched := analytics.Match(sites, appHosts(a))
		if len(matched) == 0 {
			continue
		}
		line := AppVisits{App: a.Name}
		for _, site := range matched {
			cur, prev, err := s.Analytics.Totals(ctx, site, from, to)
			if err != nil {
				umamiError(w, err)
				return
			}
			addStats(&line.Current, cur)
			addStats(&line.Previous, prev)
		}
		addStats(&out.Current, line.Current)
		addStats(&out.Previous, line.Previous)
		out.Apps = append(out.Apps, line)
	}
	sort.SliceStable(out.Apps, func(i, j int) bool { return out.Apps[i].Current.Visitors > out.Apps[j].Current.Visitors })
	writeJSON(w, out)
}
