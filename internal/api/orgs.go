package api

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// orgSlug is an organization's address: lowercase letters, digits and
// dashes, 2 to 40 characters.
var orgSlug = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])$`)

// Me is who is signed in: their role in the organization they work in, and
// whether they also run the platform (its organization's administrators).
type Me struct {
	Login    string    `json:"login"`
	Role     string    `json:"role"`
	Org      store.Org `json:"org"`
	Operator bool      `json:"operator"`
}

// me is the signed-in person, for the top bar.
func (s *Server) me(w http.ResponseWriter, r *http.Request, login string) {
	out := Me{Login: login, Role: roleOf(r)}
	o, err := s.Store.GetOrg(r.Context(), orgOf(r))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out.Org = o
	if role, err := s.Store.MemberRole(r.Context(), store.DefaultOrg, login); err == nil {
		out.Operator = role == store.MemberAdmin
	}
	writeJSON(w, out)
}

// listOrgs lists the organizations the person belongs to, with their role.
func (s *Server) listOrgs(w http.ResponseWriter, r *http.Request, login string) {
	orgs, err := s.Store.OrgsOf(r.Context(), login)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, orgs)
}

// createOrg makes an organization with the person as its administrator
// (the platform's operators only, while organizations are by invitation).
func (s *Server) createOrg(w http.ResponseWriter, r *http.Request, login string) {
	var in struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.Slug, in.Name = strings.ToLower(strings.TrimSpace(in.Slug)), strings.TrimSpace(in.Name)
	if !orgSlug.MatchString(in.Slug) || in.Name == "" || len([]rune(in.Name)) > 80 {
		httpError(w, http.StatusBadRequest, "an organization needs a name and an address of 2 to 40 lowercase letters, digits and dashes")
		return
	}
	o, err := s.Store.CreateOrg(r.Context(), in.Slug, in.Name, login)
	if errors.Is(err, store.ErrSlugTaken) {
		httpError(w, http.StatusConflict, "that address is taken by another organization")
		return
	} else if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Log.Info("organization created", "org", o.Slug, "by", login)
	writeJSONStatus(w, http.StatusCreated, o)
}

// switchOrg moves the session to another organization the person belongs to.
func (s *Server) switchOrg(w http.ResponseWriter, r *http.Request, login string) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpError(w, http.StatusBadRequest, "bad organization id")
		return
	}
	if _, err := s.Store.MemberRole(r.Context(), id, login); err != nil {
		httpError(w, http.StatusNotFound, "no such organization")
		return
	}
	c, _ := r.Cookie(sessionCookie)
	if err := s.Store.SetSessionOrg(r.Context(), hashToken(c.Value), id); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
