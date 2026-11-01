package api

import (
	"net/http"
	"slices"
	"strings"

	"github.com/p0dxD/rendimiento.ai/internal/platform"
	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// CapacityView is the capacity set on the Environment page, with what each
// value is when it is not set (the environment variables).
type CapacityView struct {
	Saved    store.Capacity `json:"saved"`
	Defaults struct {
		ParallelRuns int      `json:"parallelRuns"`
		RunsPerApp   int      `json:"runsPerApp"`
		NodesOff     []string `json:"nodesOff"` // BUILD_EXCLUDE_NODES, always off
	} `json:"defaults"`
	MaxWorkers int `json:"maxWorkers"`
}

func (s *Server) capacityView(w http.ResponseWriter, r *http.Request) {
	var v CapacityView
	var err error
	if v.Saved, err = s.Store.GetCapacity(r.Context()); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	v.MaxWorkers = platform.MaxWorkers
	if s.Platform != nil {
		v.Defaults.ParallelRuns, v.Defaults.RunsPerApp = max(s.Platform.Workers, 1), max(s.Platform.PerApp, 1)
	}
	v.Defaults.NodesOff = []string{}
	if s.Environment != nil && s.Environment.Config.ExcludeNodes != nil {
		v.Defaults.NodesOff = s.Environment.Config.ExcludeNodes
	}
	writeJSON(w, v)
}

// getCapacity is the capacity set on the Environment page.
func (s *Server) getCapacity(w http.ResponseWriter, r *http.Request, _ string) { s.capacityView(w, r) }

// putCapacity sets how much the platform runs at once: every process
// follows it within 15 seconds, this one at once.
func (s *Server) putCapacity(w http.ResponseWriter, r *http.Request, login string) {
	var in store.Capacity
	if !readJSON(w, r, &in) {
		return
	}
	if in.ParallelRuns < 0 || in.ParallelRuns > platform.MaxWorkers || in.RunsPerApp < 0 || in.RunsPerApp > platform.MaxWorkers ||
		in.StepCPUMillis < 0 || (in.StepCPUMillis > 0 && in.StepCPUMillis < 250) || in.StepCPUMillis > 64_000 || len(in.NodesOff) > 100 {
		httpError(w, http.StatusBadRequest, "runs at once and per app go from 1 to 8, and a step's CPU from 0.25 cores")
		return
	}
	nodes := []string{}
	for _, n := range in.NodesOff {
		if n = strings.TrimSpace(n); n != "" && !slices.Contains(nodes, n) {
			nodes = append(nodes, n)
		}
	}
	in.NodesOff = nodes
	if err := s.Store.SaveCapacity(r.Context(), in, login); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if s.Capacity != nil {
		if c, err := s.Store.GetCapacity(r.Context()); err == nil {
			s.Capacity.Set(c)
		}
	}
	s.Log.Info("capacity saved", "parallel", in.ParallelRuns, "perApp", in.RunsPerApp, "stepCPU", in.StepCPUMillis, "nodesOff", in.NodesOff, "by", login)
	s.capacityView(w, r)
}
