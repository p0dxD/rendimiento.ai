package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/ruta"
	"github.com/p0dxD/rendimiento.ai/internal/store"
)

var rutaKinds = map[string]bool{store.KindDecision: true, store.KindManual: true, store.KindPendiente: true, store.KindNota: true, store.KindVivido: true}

// RutaInput is what a person writes or changes on the Ruta page.
type RutaInput struct {
	Kind  string   `json:"kind"`
	Title string   `json:"title"`
	Body  string   `json:"body"`
	Tags  []string `json:"tags"`
	Done  bool     `json:"done"`
}

func (in *RutaInput) check() string {
	in.Title = strings.TrimSpace(in.Title)
	tags := []string{}
	for _, t := range in.Tags {
		if t = strings.ToLower(strings.TrimSpace(t)); t != "" {
			tags = append(tags, t)
		}
	}
	in.Tags = tags
	switch {
	case !rutaKinds[in.Kind]:
		return "unknown kind"
	case in.Title == "" || len([]rune(in.Title)) > 200:
		return "the title must be 1 to 200 characters"
	case len([]rune(in.Body)) > 50000:
		return "the text is longer than 50,000 characters"
	case len(in.Tags) > 10:
		return "at most 10 tags"
	case ruta.LooksSecret(in.Title + "\n" + in.Body + "\n" + strings.Join(in.Tags, " ")):
		return "this looks like it contains a secret (a password, token or key); the Ruta keeps no secrets"
	}
	return ""
}

func (s *Server) listRuta(w http.ResponseWriter, r *http.Request, _ string) {
	q := r.URL.Query()
	kind := q.Get("kind")
	if kind != "" && !rutaKinds[kind] {
		httpError(w, http.StatusBadRequest, "unknown kind")
		return
	}
	list, err := s.Store.SearchRuta(r.Context(), kind, strings.TrimSpace(q.Get("q")), 500)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, list)
}

func (s *Server) addRuta(w http.ResponseWriter, r *http.Request, login string) {
	var in RutaInput
	if !readJSON(w, r, &in) {
		return
	}
	if msg := in.check(); msg != "" {
		httpError(w, http.StatusBadRequest, msg)
		return
	}
	e, err := s.Store.AddRuta(r.Context(), store.RutaEntry{Kind: in.Kind, Title: in.Title, Body: in.Body, Tags: in.Tags, Done: in.Done, Author: login})
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSONStatus(w, http.StatusCreated, e)
}

func rutaID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpError(w, http.StatusBadRequest, "bad entry id")
		return 0, false
	}
	return id, true
}

func (s *Server) updateRuta(w http.ResponseWriter, r *http.Request, login string) {
	id, ok := rutaID(w, r)
	if !ok {
		return
	}
	var in RutaInput
	if !readJSON(w, r, &in) {
		return
	}
	e, err := s.Store.Ruta(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		httpError(w, http.StatusNotFound, "no such entry")
		return
	} else if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	in.Kind = e.Kind // the kind never changes
	if msg := in.check(); msg != "" {
		httpError(w, http.StatusBadRequest, msg)
		return
	}
	e.Title, e.Body, e.Tags, e.Done = in.Title, in.Body, in.Tags, in.Done
	e, err = s.Store.UpdateRuta(r.Context(), e, login)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, e)
}

func (s *Server) archiveRuta(w http.ResponseWriter, r *http.Request, login string) {
	id, ok := rutaID(w, r)
	if !ok {
		return
	}
	if err := s.Store.ArchiveRuta(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		httpError(w, http.StatusNotFound, "no such entry")
		return
	} else if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Log.Info("ruta entry archived", "id", id, "by", login)
	w.WriteHeader(http.StatusNoContent)
}

// RutaActivity is the agents' side of the Ruta page: their turns and what they did.
type RutaActivity struct {
	Cargos  []store.Cargo          `json:"cargos"`
	Actions []store.AgentActionRow `json:"actions"`
}

func (s *Server) rutaActivity(w http.ResponseWriter, r *http.Request, _ string) {
	cargos, err := s.Store.ListCargos(r.Context(), 100)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	actions, err := s.Store.ListAgentActions(r.Context(), 200)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, RutaActivity{Cargos: cargos, Actions: actions})
}

// ---- agent keys ----

// NewAgentKey asks for a key.
type NewAgentKey struct {
	Name     string `json:"name"`
	CanWrite bool   `json:"canWrite"`
	Days     int    `json:"days"` // 1 to 90
}

// CreatedAgentKey is the answer: the token appears here once and never again.
type CreatedAgentKey struct {
	Key      store.AgentKey `json:"key"`
	Token    string         `json:"token"`
	Endpoint string         `json:"endpoint"`
}

func (s *Server) listAgentKeys(w http.ResponseWriter, r *http.Request, _ string) {
	keys, err := s.Store.ListAgentKeys(r.Context())
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, keys)
}

func (s *Server) createAgentKey(w http.ResponseWriter, r *http.Request, login string) {
	var in NewAgentKey
	if !readJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len([]rune(in.Name)) > 60 {
		httpError(w, http.StatusBadRequest, "the name must be 1 to 60 characters")
		return
	}
	if in.Days < 1 || time.Duration(in.Days)*24*time.Hour > ruta.MaxKeyLifetime {
		httpError(w, http.StatusBadRequest, "a key lives 1 to 90 days")
		return
	}
	token, hash, err := ruta.NewToken()
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	k, err := s.Store.CreateAgentKey(r.Context(), in.Name, hash, in.CanWrite, login, time.Now().Add(time.Duration(in.Days)*24*time.Hour))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Log.Info("agent key created", "id", k.ID, "name", k.Name, "write", k.CanWrite, "by", login)
	writeJSONStatus(w, http.StatusCreated, CreatedAgentKey{Key: k, Token: token, Endpoint: strings.TrimRight(s.BaseURL, "/") + "/mcp"})
}

func (s *Server) revokeAgentKey(w http.ResponseWriter, r *http.Request, login string) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpError(w, http.StatusBadRequest, "bad key id")
		return
	}
	if err := s.Store.RevokeAgentKey(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		httpError(w, http.StatusNotFound, "no such key, or already revoked")
		return
	} else if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Log.Info("agent key revoked", "id", id, "by", login)
	w.WriteHeader(http.StatusNoContent)
}
