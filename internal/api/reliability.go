package api

import (
	"net/http"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// reliabilityRanges maps the ranges the UI offers to their chart bucket:
// about 96, 168 and 120 points.
var reliabilityRanges = map[string]struct{ span, bucket time.Duration }{
	"24h": {24 * time.Hour, 15 * time.Minute},
	"7d":  {7 * 24 * time.Hour, time.Hour},
	"30d": {30 * 24 * time.Hour, 6 * time.Hour},
}

type releaseMark struct {
	Number     int64     `json:"number"`
	At         time.Time `json:"at"`
	SHA        string    `json:"sha"`
	RollbackOf *int64    `json:"rollbackOf,omitempty"`
	// VerifyStatus is what release verification concluded.
	VerifyStatus string `json:"verifyStatus,omitempty"`
}

type reliabilityView struct {
	Range     string               `json:"range"`
	Since     time.Time            `json:"since"`
	BucketSec int                  `json:"bucketSeconds"`
	Series    []store.UptimeSeries `json:"series"`
	Incidents []store.Incident     `json:"incidents"`
	Releases  []releaseMark        `json:"releases"`
}

// reliability returns an app's uptime checks over a range (24h, 7d or 30d):
// per check, uptime, response times and chart buckets; its outages; and its
// releases, to mark on the charts.
func (s *Server) reliability(w http.ResponseWriter, r *http.Request, _ string) {
	a := s.app(w, r)
	if a == nil {
		return
	}
	name := r.URL.Query().Get("range")
	if name == "" {
		name = "24h"
	}
	rg, ok := reliabilityRanges[name]
	if !ok {
		httpError(w, http.StatusBadRequest, "range must be 24h, 7d or 30d")
		return
	}
	since := time.Now().Add(-rg.span)
	series, err := s.Store.Uptime(r.Context(), a.ID, since, rg.bucket)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	incidents, err := s.Store.Incidents(r.Context(), a.ID, since)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rels, err := s.Store.ListReleases(r.Context(), a.ID, 200)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	marks := []releaseMark{}
	for _, rel := range rels {
		if rel.CreatedAt.After(since) {
			marks = append(marks, releaseMark{Number: rel.Number, At: rel.CreatedAt, SHA: rel.SHA, RollbackOf: rel.RollbackOf, VerifyStatus: rel.VerifyStatus})
		}
	}
	writeJSON(w, reliabilityView{Range: name, Since: since, BucketSec: int(rg.bucket.Seconds()),
		Series: series, Incidents: incidents, Releases: marks})
}
