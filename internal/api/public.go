package api

import (
	"context"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// PublicStats is what GET /api/public/stats returns: aggregate numbers for
// a public page (a portfolio, a status page). Only public site names and
// URLs, node names and counts; no addresses, secrets or commit messages.
type PublicStats struct {
	GeneratedAt time.Time            `json:"generatedAt"`
	TimeZone    string               `json:"timeZone"`
	Delivery    *store.DeliveryStats `json:"delivery30d"`
	Sites       []publicSite         `json:"sites"`
	Cluster     publicCluster        `json:"cluster"`
	Recent      []store.ReleaseEvent `json:"recent"`
}

type publicSite struct {
	store.SiteStats
	URL string `json:"url"`
}

type publicNode struct {
	Name     string   `json:"name"`
	Role     string   `json:"role"` // control-plane or worker
	Arch     string   `json:"arch"`
	OS       string   `json:"os"`
	Ready    bool     `json:"ready"`
	Pods     int      `json:"pods"`
	CPUCores float64  `json:"cpuCores"`
	CPUUsed  *float64 `json:"cpuUsed,omitempty"`
	MemBytes int64    `json:"memBytes"`
	MemUsed  *int64   `json:"memUsed,omitempty"`
	GPUs     int64    `json:"gpus,omitempty"`
}

type publicCluster struct {
	Distribution string       `json:"distribution"`
	Version      string       `json:"version"`
	Pods         int          `json:"pods"`
	Apps         int          `json:"apps"`
	Nodes        []publicNode `json:"nodes"`
}

// publicCache keeps the last answer for a minute, so visitors cannot make
// the platform recompute it on every page view.
type publicCache struct {
	mu    sync.Mutex
	at    time.Time
	stats *PublicStats
}

// publicStats serves PublicStats when PublicStats is on (no login needed).
// Browsers on PublicOrigins may fetch it directly (CORS).
func (s *Server) publicStats(w http.ResponseWriter, r *http.Request) {
	if !s.PublicStats {
		httpError(w, http.StatusNotFound, "public stats are off (PUBLIC_STATS)")
		return
	}
	if o := r.Header.Get("Origin"); o != "" && slices.Contains(s.PublicOrigins, o) {
		w.Header().Set("Access-Control-Allow-Origin", o)
		w.Header().Set("Vary", "Origin")
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
	s.public.mu.Lock()
	defer s.public.mu.Unlock()
	if s.public.stats == nil || time.Since(s.public.at) > time.Minute {
		st, err := s.buildPublicStats(r.Context())
		if err != nil {
			if s.public.stats == nil {
				httpError(w, http.StatusInternalServerError, "stats unavailable")
				return
			}
			s.Log.Warn("public stats", "err", err) // serve the previous ones
		} else {
			s.public.stats, s.public.at = st, time.Now()
		}
	}
	writeJSON(w, s.public.stats)
}

func (s *Server) buildPublicStats(ctx context.Context) (*PublicStats, error) {
	tz := s.PublicTimeZone
	if tz == "" {
		tz = "UTC"
	}
	out := &PublicStats{GeneratedAt: time.Now(), TimeZone: tz, Sites: []publicSite{}}
	var err error
	if out.Delivery, err = s.Store.Delivery(ctx, 30, tz); err != nil {
		return nil, err
	}
	if out.Recent, err = s.Store.RecentReleases(ctx, 12); err != nil {
		return nil, err
	}
	sites, err := s.Store.PublicSites(ctx, 90, tz)
	if err != nil {
		return nil, err
	}
	apps, err := s.Store.ListApps(ctx)
	if err != nil {
		return nil, err
	}
	out.Cluster.Apps = len(apps)
	domain := map[[2]string]string{}
	for _, a := range apps {
		for _, svc := range a.Spec.Services {
			if svc.Domain != "" {
				domain[[2]string{a.Name, svc.Name}] = "https://" + svc.Domain
			}
		}
	}
	for _, st := range sites {
		if url := domain[[2]string{st.App, st.Service}]; url != "" {
			out.Sites = append(out.Sites, publicSite{SiteStats: st, URL: url})
		}
	}
	if s.Environment != nil {
		rep := s.Environment.Report(ctx, false)
		out.Cluster.Distribution, out.Cluster.Version, out.Cluster.Pods = rep.Cluster.Name, rep.Cluster.Version, rep.Cluster.Pods
		for _, n := range rep.Nodes {
			role := "worker"
			if slices.Contains(n.Roles, "control-plane") || slices.Contains(n.Roles, "master") {
				role = "control-plane"
			}
			out.Cluster.Nodes = append(out.Cluster.Nodes, publicNode{Name: n.Name, Role: role, Arch: n.Arch, OS: n.OS, Ready: n.Ready,
				Pods: n.Pods, CPUCores: n.CPUCores, CPUUsed: n.CPUUsed, MemBytes: n.MemBytes, MemUsed: n.MemUsed, GPUs: n.GPUs})
		}
	}
	return out, nil
}
