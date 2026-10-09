package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/problems"
	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// ProblemList is the Problems page: what went wrong in the platform in the
// last 30 days, newest first, and how many problems are open.
type ProblemList struct {
	Open     int             `json:"open"`
	OpenFor  int             `json:"openForHours"` // a problem is open this long after it last happened
	Problems []store.Problem `json:"problems"`
}

// listProblems is the Problems page: warnings and errors the platform
// logged recently (an app's, or all), repeats grouped.
func (s *Server) listProblems(w http.ResponseWriter, r *http.Request, _ string) {
	q := r.URL.Query()
	list, err := s.Store.ListProblems(r.Context(), q.Get("app"), time.Now().Add(-problems.Keep), q.Get("dismissed") == "1", 300)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	open, err := s.Store.OpenProblems(r.Context())
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, ProblemList{Open: open, OpenFor: int(store.ProblemOpenFor.Hours()), Problems: list})
}

// problemCount is the navigation badge's number.
func (s *Server) problemCount(w http.ResponseWriter, r *http.Request, _ string) {
	open, err := s.Store.OpenProblems(r.Context())
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]int{"open": open})
}

// dismissProblem hides a problem until it happens again.
func (s *Server) dismissProblem(w http.ResponseWriter, r *http.Request, login string) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpError(w, http.StatusBadRequest, "bad problem id")
		return
	}
	if err := s.Store.DismissProblem(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		httpError(w, http.StatusNotFound, "no such problem")
		return
	} else if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Log.Info("problem dismissed", "id", id, "by", login)
	w.WriteHeader(http.StatusNoContent)
}
