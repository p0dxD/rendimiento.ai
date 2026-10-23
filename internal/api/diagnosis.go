package api

import (
	"errors"
	"net/http"

	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// getDiagnosis is a failed run's diagnosis by the diagnostician agent:
// why it failed and how to fix it (404 when there is none yet).
func (s *Server) getDiagnosis(w http.ResponseWriter, r *http.Request, _ string) {
	id, ok := s.runID(w, r)
	if !ok {
		return
	}
	d, err := s.Store.GetDiagnosis(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		httpError(w, http.StatusNotFound, "this run has no diagnosis")
		return
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, d)
}

// askDiagnosis asks the diagnostician agent to explain a failed run (again).
func (s *Server) askDiagnosis(w http.ResponseWriter, r *http.Request, login string) {
	id, ok := s.runID(w, r)
	if !ok {
		return
	}
	run, err := s.Store.GetRun(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		httpError(w, http.StatusNotFound, "no such run")
		return
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if run.Status != store.RunFailed {
		httpError(w, http.StatusConflict, "only a failed run is diagnosed")
		return
	}
	if err := s.Store.AskDiagnosis(r.Context(), id, login); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
