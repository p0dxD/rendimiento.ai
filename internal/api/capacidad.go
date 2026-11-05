package api

import (
	"net/http"
	"slices"
	"strings"

	"github.com/rendimiento-ai/rendimiento/internal/platform"
	"github.com/rendimiento-ai/rendimiento/internal/store"
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
	// Busy are the nodes too full right now to take new steps or builds.
	Busy               []string `json:"busy"`
	DefaultBusyPercent int      `json:"defaultBusyPercent"`
}

func (s *Server) capacityView(w http.ResponseWriter, r *http.Request) {
	var v CapacityView
	var err error
	if v.Saved, err = s.Store.GetCapacity(r.Context()); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	v.MaxWorkers, v.DefaultBusyPercent, v.Busy = platform.MaxWorkers, platform.DefaultBusyPercent, []string{}
	if s.Capacity != nil {
		v.Busy = s.Capacity.Busy()
	}
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
		in.StepCPUMillis < 0 || (in.StepCPUMillis > 0 && in.StepCPUMillis < 250) || in.StepCPUMillis > 64_000 || len(in.NodesOff) > 100 ||
		in.BusyPercent < 0 || (in.BusyPercent > 0 && in.BusyPercent < 50) || in.BusyPercent > 100 {
		httpError(w, http.StatusBadRequest, "runs at once and per app go from 1 to 8, a step's CPU from 0.25 cores, and a busy node from 50% to 100%")
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
	s.Log.Info("capacity saved", "parallel", in.ParallelRuns, "perApp", in.RunsPerApp, "stepCPU", in.StepCPUMillis, "nodesOff", in.NodesOff, "busy", in.BusyPercent, "by", login)
	s.capacityView(w, r)
}
