package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/agentes"
	"github.com/p0dxD/rendimiento.ai/internal/ruta"
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

// DiagnosisFeedback is what a person says of a diagnosis: it helped, or it
// was wrong and the real cause was Cause.
type DiagnosisFeedback struct {
	Useful bool   `json:"useful"`
	Cause  string `json:"cause"`
}

// diagnosisFeedback keeps what a person says of a diagnosis, and turns it
// into a known failure in the Ruta (tagged falla-conocida and the app),
// which the diagnostician reads when a similar failure comes: that is how
// it learns. Saying it again updates the same entry.
func (s *Server) diagnosisFeedback(w http.ResponseWriter, r *http.Request, login string) {
	id, ok := s.runID(w, r)
	if !ok {
		return
	}
	var in DiagnosisFeedback
	if !readJSON(w, r, &in) {
		return
	}
	in.Cause = strings.TrimSpace(in.Cause)
	switch {
	case !in.Useful && in.Cause == "":
		httpError(w, http.StatusBadRequest, "say what the real cause was, so the diagnostician learns it")
		return
	case len([]rune(in.Cause)) > 4000:
		httpError(w, http.StatusBadRequest, "the cause is too long (4,000 characters at most)")
		return
	case ruta.LooksSecret(in.Cause):
		httpError(w, http.StatusBadRequest, "this looks like it contains a secret; say where it lives instead of its value")
		return
	}
	ctx := r.Context()
	d, err := s.Store.GetDiagnosis(ctx, id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && d.Status != store.DiagnosisDone) {
		httpError(w, http.StatusConflict, "there is no explanation to judge yet")
		return
	} else if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	run, err := s.Store.GetRun(ctx, id)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	app, err := s.Store.GetAppByID(ctx, run.AppID)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	entry := knownFailure(app, run, d, in, login)
	feedback := "useful"
	if !in.Useful {
		feedback = "wrong"
	}
	var saved store.RutaEntry
	if d.RutaEntry != nil {
		if prev, err := s.Store.Ruta(ctx, *d.RutaEntry); err == nil && prev.OrgID == app.OrgID {
			prev.Title, prev.Body, prev.Tags = entry.Title, entry.Body, entry.Tags
			saved, err = s.Store.UpdateRuta(ctx, prev, login)
			if err != nil {
				httpError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
	}
	if saved.ID == 0 {
		if saved, err = s.Store.AddRuta(ctx, entry); err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := s.Store.SetDiagnosisFeedback(ctx, id, feedback, login, saved.ID); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Log.Info("diagnosis judged", "run", id, "feedback", feedback, "ruta", saved.ID, "by", login)
	writeJSON(w, map[string]any{"feedback": feedback, "rutaEntry": saved.ID})
}

// knownFailure is the Ruta entry a judged diagnosis becomes, in the
// Ruta's language (Mexican Spanish): the telling line, and the cause that
// was confirmed or corrected.
func knownFailure(app *store.App, run *store.Run, d store.Diagnosis, in DiagnosisFeedback, login string) store.RutaEntry {
	line := d.Signature
	if line == "" {
		line = run.Message
	}
	title := line
	if title == "" {
		title = in.Cause
	}
	if r := []rune(title); len(r) > 160 {
		title = string(r[:160]) + "…"
	}
	var b strings.Builder
	if in.Useful {
		fmt.Fprintf(&b, "%s confirmó esta explicación en la ejecución #%d de %s (rama %s), el %s.\n\n", login, run.ID, app.Name, run.Branch, time.Now().Format("2006-01-02"))
	} else {
		fmt.Fprintf(&b, "%s corrigió al diagnosticador en la ejecución #%d de %s (rama %s), el %s.\n\n**La causa real:** %s\n\n", login, run.ID, app.Name, run.Branch, time.Now().Format("2006-01-02"), in.Cause)
	}
	if line != "" {
		fmt.Fprintf(&b, "**La línea que la muestra:**\n\n```\n%s\n```\n\n", line)
	}
	if in.Useful {
		fmt.Fprintf(&b, "**La explicación que sirvió:**\n\n%s\n", d.Text)
	} else {
		fmt.Fprintf(&b, "**Lo que el diagnosticador había dicho (no sirvió):**\n\n%s\n", d.Text)
	}
	return store.RutaEntry{Kind: store.KindManual, Title: "Falla conocida: " + title, Body: b.String(),
		Tags: []string{agentes.KnownTag, strings.ToLower(app.Name)}, Author: login, OrgID: app.OrgID}
}
