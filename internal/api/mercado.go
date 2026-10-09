package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	gh "github.com/p0dxD/rendimiento.ai/internal/github"
	"github.com/p0dxD/rendimiento.ai/internal/mercado"
	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// maxPagina bounds a page sent from the form, photos included (the form
// shrinks photos first; the ingress takes up to 25 MB).
const maxPagina = 24 << 20

// puestoView is a page in the Mercado: its card, and its app's state.
type puestoView struct {
	*store.Puesto
	appView
}

type mercadoView struct {
	Enabled bool         `json:"enabled"`
	Org     string       `json:"org,omitempty"`
	Puestos []puestoView `json:"puestos"`
}

// listMercado is the Mercado page: whether it is on, and every stall with
// its app's state.
func (s *Server) listMercado(w http.ResponseWriter, r *http.Request, _ string) {
	out := mercadoView{Puestos: []puestoView{}}
	if s.Mercado != nil {
		out.Enabled, out.Org = true, s.Mercado.Org
	}
	puestos, err := s.Store.ListPuestos(r.Context())
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	down := s.downChecks(r.Context())
	for _, p := range puestos {
		a, err := s.Store.GetAppByID(r.Context(), p.AppID)
		if err != nil {
			continue
		}
		v := s.view(r.Context(), a)
		v.Down = down[a.ID]
		out.Puestos = append(out.Puestos, puestoView{Puesto: p, appView: v})
	}
	writeJSON(w, out)
}

// readPagina decodes a page from the form, photos and all.
func readPagina(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, maxPagina)).Decode(v); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

// previewMercado renders the page as the form fills it. The HTML comes
// back inside JSON, for a sandboxed frame, never as a page of this site.
func (s *Server) previewMercado(w http.ResponseWriter, r *http.Request, _ string) {
	var req struct {
		Pagina  mercado.Pagina `json:"pagina"`
		Dominio string         `json:"dominio"`
	}
	if !readPagina(w, r, &req) {
		return
	}
	req.Pagina.Default()
	if req.Pagina.Nombre == "" {
		req.Pagina.Nombre = "…"
	}
	base := ""
	if req.Dominio != "" {
		base = "https://" + req.Dominio + "/"
	}
	html, err := mercado.Render(&req.Pagina, base)
	if err != nil {
		httpError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, map[string]string{"html": string(html)})
}

// openPuesto opens a stall from the form: its repository, app and first
// version (see mercado.Abrir).
func (s *Server) openPuesto(w http.ResponseWriter, r *http.Request, login string) {
	if s.Mercado == nil {
		httpError(w, http.StatusServiceUnavailable, "the Mercado is not set up")
		return
	}
	var req mercado.Abrir
	if !readPagina(w, r, &req) {
		return
	}
	app, err := s.Mercado.Abrir(r.Context(), login, req)
	if errors.Is(err, gh.ErrNotConfigured) {
		httpError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	if err != nil {
		httpError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	s.Log.Info("page opened", "app", app.Name, "repo", app.Repo, "by", login)
	writeJSONStatus(w, http.StatusCreated, app)
}

// puesto finds the page named in the path, or answers 404.
func (s *Server) puesto(w http.ResponseWriter, r *http.Request) (*store.App, *store.Puesto) {
	a := s.app(w, r)
	if a == nil {
		return nil, nil
	}
	p, err := s.Store.GetPuesto(r.Context(), a.ID)
	if errors.Is(err, store.ErrNotFound) {
		httpError(w, http.StatusNotFound, "this app is not a page of the Mercado")
		return nil, nil
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return nil, nil
	}
	return a, p
}

// getPuesto is a stall's page as its form edits it.
func (s *Server) getPuesto(w http.ResponseWriter, r *http.Request, _ string) {
	if s.Mercado == nil {
		httpError(w, http.StatusServiceUnavailable, "the Mercado is not set up")
		return
	}
	a, p := s.puesto(w, r)
	if a == nil {
		return
	}
	pagina, err := s.Mercado.Leer(r.Context(), a)
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	dominio := ""
	if len(a.Spec.Services) > 0 {
		dominio = a.Spec.Services[0].Domain
	}
	writeJSON(w, map[string]any{"puesto": p, "pagina": pagina, "dominio": dominio, "repo": a.Repo})
}

// savePuesto saves a changed page; its build and deploy follow on their own.
func (s *Server) savePuesto(w http.ResponseWriter, r *http.Request, login string) {
	if s.Mercado == nil {
		httpError(w, http.StatusServiceUnavailable, "the Mercado is not set up")
		return
	}
	a, _ := s.puesto(w, r)
	if a == nil {
		return
	}
	var req struct {
		Pagina mercado.Pagina `json:"pagina"`
	}
	if !readPagina(w, r, &req) {
		return
	}
	if err := s.Mercado.Guardar(r.Context(), a, req.Pagina); err != nil {
		httpError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	s.Log.Info("page saved", "app", a.Name, "by", login)
	w.WriteHeader(http.StatusNoContent)
}
