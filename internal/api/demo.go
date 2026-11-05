package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rendimiento-ai/rendimiento/internal/store"
)

// demoSession is the longest a demo session lasts; opening the link again
// starts another, until the link expires.
const demoSession = 7 * 24 * time.Hour

// openDemo is a demo link: GET /demo/{code} signs the browser in as the
// link's guest, read-only in its organization, and goes to the apps. An
// unknown, expired or turned-off code is a 404 like any other.
func (s *Server) openDemo(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if len(code) < 32 || len(code) > 128 {
		httpError(w, http.StatusNotFound, "this demo link does not work: it expired or was turned off")
		return
	}
	ctx := r.Context()
	d, err := s.Store.OpenDemoLink(ctx, hashToken(code))
	if errors.Is(err, store.ErrNotFound) {
		httpError(w, http.StatusNotFound, "this demo link does not work: it expired or was turned off")
		return
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	ttl := min(demoSession, time.Until(d.ExpiresAt))
	token := randomToken()
	login := store.DemoLogin(d.ID)
	if err := s.Store.CreateSession(ctx, hashToken(token), login, ttl); err != nil {
		httpError(w, http.StatusInternalServerError, "could not create session")
		return
	}
	if err := s.Store.SetSessionOrg(ctx, hashToken(token), d.OrgID); err != nil {
		httpError(w, http.StatusInternalServerError, "could not create session")
		return
	}
	s.Log.Info("demo link opened", "link", d.ID, "org", d.OrgID, "uses", d.Uses)
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", MaxAge: int(ttl.Seconds()), HttpOnly: true, Secure: s.secureCookies(), SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/", http.StatusFound)
}

// CreatedDemoLink is a new demo link with its address, shown only once.
type CreatedDemoLink struct {
	store.DemoLink
	URL string `json:"url"`
}

// listDemoLinks lists the organization's demo links (never their codes).
func (s *Server) listDemoLinks(w http.ResponseWriter, r *http.Request, _ string) {
	ds, err := s.Store.DemoLinks(r.Context(), orgOf(r))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if ds == nil {
		ds = []store.DemoLink{}
	}
	writeJSON(w, ds)
}

// createDemoLink makes a demo link for the organization, for 1 to 90 days.
// The platform's own organization has none: its guests see the cluster.
func (s *Server) createDemoLink(w http.ResponseWriter, r *http.Request, login string) {
	var in struct {
		Note string `json:"note"`
		Days int    `json:"days"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Days < 1 || in.Days > 90 {
		httpError(w, http.StatusBadRequest, "a demo link lasts from 1 to 90 days")
		return
	}
	note := strings.TrimSpace(in.Note)
	if len(note) > 120 {
		httpError(w, http.StatusBadRequest, "the note is too long (120 characters at most)")
		return
	}
	code := randomToken()
	d, err := s.Store.CreateDemoLink(r.Context(), orgOf(r), hashToken(code), note, login, time.Now().Add(time.Duration(in.Days)*24*time.Hour))
	if errors.Is(err, store.ErrDemoPlatformOrg) {
		httpError(w, http.StatusConflict, "demo links are for another organization, not the platform's: its guests would see the cluster; make one for the demo and switch to it")
		return
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Log.Info("demo link created", "link", d.ID, "org", d.OrgID, "days", in.Days, "by", login)
	writeJSONStatus(w, http.StatusCreated, CreatedDemoLink{DemoLink: *d, URL: strings.TrimRight(s.BaseURL, "/") + "/demo/" + code})
}

// deleteDemoLink turns a demo link off; whoever opened it loses access at
// once.
func (s *Server) deleteDemoLink(w http.ResponseWriter, r *http.Request, login string) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpError(w, http.StatusBadRequest, "bad demo link id")
		return
	}
	if err := s.Store.DeleteDemoLink(r.Context(), orgOf(r), id); errors.Is(err, store.ErrNotFound) {
		httpError(w, http.StatusNotFound, "no such demo link")
		return
	} else if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Log.Info("demo link turned off", "link", id, "by", login)
	w.WriteHeader(http.StatusNoContent)
}
