package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/p0dxD/rendimiento.ai/internal/ruta"
	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// NewEncargo asks agents for something: what, the detail, and the app or
// run it is about.
type NewEncargo struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	App   string `json:"app"`
	RunID *int64 `json:"runId"`
}

// listEncargos lists the open encargos (?all=1: also reviewed and cancelled).
func (s *Server) listEncargos(w http.ResponseWriter, r *http.Request, _ string) {
	list, err := s.Store.ListEncargos(r.Context(), r.URL.Query().Get("all") != "1", 100)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []store.Encargo{}
	}
	writeJSON(w, list)
}

// createEncargo leaves work for agents, pending until one takes it.
func (s *Server) createEncargo(w http.ResponseWriter, r *http.Request, login string) {
	var in NewEncargo
	if !readJSON(w, r, &in) {
		return
	}
	in.Title, in.App = strings.TrimSpace(in.Title), strings.TrimSpace(in.App)
	if in.Title == "" || len([]rune(in.Title)) > 200 || len([]rune(in.Body)) > 20000 {
		httpError(w, http.StatusBadRequest, "the title must be 1 to 200 characters, and the detail up to 20,000")
		return
	}
	if ruta.LooksSecret(in.Title) || ruta.LooksSecret(in.Body) {
		httpError(w, http.StatusBadRequest, "this looks like it contains a secret; say where it lives instead of its value")
		return
	}
	if in.RunID != nil {
		run, err := s.Store.GetRun(r.Context(), *in.RunID)
		if err != nil {
			httpError(w, http.StatusBadRequest, "no such run")
			return
		}
		if a, err := s.Store.GetAppByID(r.Context(), run.AppID); err == nil && in.App == "" {
			in.App = a.Name
		}
	}
	if in.App != "" {
		if _, err := s.Store.GetApp(r.Context(), in.App); err != nil {
			httpError(w, http.StatusBadRequest, "no such app: "+in.App)
			return
		}
	}
	e, err := s.Store.CreateEncargo(r.Context(), store.Encargo{Title: in.Title, Body: in.Body, App: in.App, RunID: in.RunID, CreatedBy: login})
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Log.Info("encargo created", "id", e.ID, "app", e.App, "by", login)
	writeJSONStatus(w, http.StatusCreated, e)
}

// encargoID reads {id}, or answers 400.
func encargoID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpError(w, http.StatusBadRequest, "bad encargo id")
	}
	return id, err == nil
}

// getEncargo is one encargo with its history.
func (s *Server) getEncargo(w http.ResponseWriter, r *http.Request, _ string) {
	id, ok := encargoID(w, r)
	if !ok {
		return
	}
	e, err := s.Store.Encargo(r.Context(), id)
	s.encargoAnswer(w, e, err)
}

// reviewEncargo is the person's answer to a delivered encargo: accepted,
// or sent back with a comment for the next agent.
func (s *Server) reviewEncargo(w http.ResponseWriter, r *http.Request, login string) {
	id, ok := encargoID(w, r)
	if !ok {
		return
	}
	var in struct {
		Accept  bool   `json:"accept"`
		Comment string `json:"comment"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.Comment = strings.TrimSpace(in.Comment)
	if !in.Accept && in.Comment == "" {
		httpError(w, http.StatusBadRequest, "say what is missing, for the agent that takes it next")
		return
	}
	e, err := s.Store.ReviewEncargo(r.Context(), id, login, in.Accept, in.Comment)
	s.encargoAnswer(w, e, err)
}

// cancelEncargo cancels an encargo that is not finished.
func (s *Server) cancelEncargo(w http.ResponseWriter, r *http.Request, login string) {
	id, ok := encargoID(w, r)
	if !ok {
		return
	}
	e, err := s.Store.CancelEncargo(r.Context(), id, login, "")
	s.encargoAnswer(w, e, err)
}

func (s *Server) encargoAnswer(w http.ResponseWriter, e store.Encargo, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		httpError(w, http.StatusNotFound, "no such encargo")
	case errors.Is(err, store.ErrEncargoState):
		httpError(w, http.StatusConflict, "the encargo is not in a state that allows this")
	case err != nil:
		httpError(w, http.StatusInternalServerError, err.Error())
	default:
		writeJSON(w, e)
	}
}
