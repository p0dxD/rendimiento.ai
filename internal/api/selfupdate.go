package api

import (
	"encoding/json"
	"net/http"
)

// platformVersion says which release of rendimiento runs and whether a newer
// one is ready (the Environment page's "Update" button).
func (s *Server) platformVersion(w http.ResponseWriter, r *http.Request, _ string) {
	if s.Update == nil {
		writeJSON(w, map[string]any{"enabled": false})
		return
	}
	st, err := s.Update.Status(r.Context())
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]any{"enabled": true, "status": st})
}

// updatePlatform switches rendimiento to a release's image; this process is
// replaced a few seconds after it answers.
func (s *Server) updatePlatform(w http.ResponseWriter, r *http.Request, login string) {
	if s.Update == nil {
		httpError(w, http.StatusNotFound, "updates from the platform's own releases are off (SELF_APP)")
		return
	}
	var body struct {
		Release int64 `json:"release"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Release <= 0 {
		httpError(w, http.StatusBadRequest, "release required")
		return
	}
	v, err := s.Update.Update(r.Context(), body.Release, login)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.Log.Info("updating rendimiento", "release", v.Release, "sha", v.SHA, "image", v.Image, "by", login)
	writeJSONStatus(w, http.StatusAccepted, v)
}
