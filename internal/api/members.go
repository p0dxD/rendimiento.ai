package api

import (
	"errors"
	"net/http"
	"regexp"

	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// githubLogin is a GitHub username: letters, digits and single dashes, up
// to 39 characters.
var githubLogin = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9]|-[A-Za-z0-9]){0,38}$`)

// listMembers lists the organization's people and their roles.
func (s *Server) listMembers(w http.ResponseWriter, r *http.Request, _ string) {
	ms, err := s.Store.Members(r.Context(), orgOf(r))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if ms == nil {
		ms = []store.Member{}
	}
	writeJSON(w, ms)
}

// putMember adds a person by their GitHub login, or changes their role.
// They can sign in from then on.
func (s *Server) putMember(w http.ResponseWriter, r *http.Request, login string) {
	who := r.PathValue("login")
	if !githubLogin.MatchString(who) {
		httpError(w, http.StatusBadRequest, "that is not a GitHub username")
		return
	}
	var in struct {
		Role string `json:"role"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if store.RoleRank(in.Role) == 0 {
		httpError(w, http.StatusBadRequest, "role must be guest, collaborator or admin")
		return
	}
	if err := s.Store.SetMember(r.Context(), orgOf(r), who, in.Role, login); errors.Is(err, store.ErrLastAdmin) {
		httpError(w, http.StatusConflict, "an organization needs at least one administrator")
		return
	} else if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Log.Info("member set", "member", who, "role", in.Role, "by", login)
	s.listMembers(w, r, login)
}

// deleteMember takes a person out; their sessions stop working at once.
func (s *Server) deleteMember(w http.ResponseWriter, r *http.Request, login string) {
	who := r.PathValue("login")
	switch err := s.Store.RemoveMember(r.Context(), orgOf(r), who); {
	case errors.Is(err, store.ErrLastAdmin):
		httpError(w, http.StatusConflict, "an organization needs at least one administrator")
		return
	case errors.Is(err, store.ErrNotFound):
		httpError(w, http.StatusNotFound, "no such member")
		return
	case err != nil:
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Log.Info("member removed", "member", who, "by", login)
	w.WriteHeader(http.StatusNoContent)
}
