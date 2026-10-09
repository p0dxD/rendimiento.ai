package api

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/analytics"
	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// visitRanges are the Visits tab's ranges and how their charts are bucketed.
var visitRanges = map[string]struct {
	span time.Duration
	unit string
}{
	"24h": {24 * time.Hour, "hour"},
	"7d":  {7 * 24 * time.Hour, "day"},
	"30d": {30 * 24 * time.Hour, "day"},
}

// visitDays is the dashboard summary's period.
const visitDays = 7

// VisitsView is an app's Visits tab: one report per Umami website whose
// domain is one of the app's hostnames.
type VisitsView struct {
	Configured bool                `json:"configured"` // rendimiento can read Umami
	Range      string              `json:"range"`
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
// over the last 24 hours, 7 or 30 days.
func (s *Server) visits(w http.ResponseWriter, r *http.Request, _ string) {
	a := s.app(w, r)
	if a == nil {
		return
	}
	name := r.URL.Query().Get("range")
	if name == "" {
		name = "7d"
	}
	rg, ok := visitRanges[name]
	if !ok {
		httpError(w, http.StatusBadRequest, "range must be 24h, 7d or 30d")
		return
	}
	tz := s.visitTZ()
	view := VisitsView{Configured: s.Analytics != nil, Range: name, TimeZone: tz.String(), Hosts: appHosts(a), UmamiURL: s.AnalyticsURL, Reports: []*analytics.Report{}}
	if s.Analytics == nil {
		writeJSON(w, view)
		return
	}
	sites, err := s.Analytics.Websites(r.Context())
	if err != nil {
		umamiError(w, err)
		return
	}
	to := time.Now()
	period := analytics.Period{From: to.Add(-rg.span), To: to, Unit: rg.unit, TZ: tz}
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
	apps, err := s.Store.ListApps(ctx)
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
