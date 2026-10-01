package api

import (
	"net/http"
	"strings"

	"github.com/p0dxD/rendimiento.ai/internal/notify"
)

// testNotification sends a sample email now, so the setup can be checked
// from the Environment page. It reports the provider's error if any.
func (s *Server) testNotification(w http.ResponseWriter, r *http.Request, login string) {
	err := s.Notify.Send(r.Context(), notify.Message{
		Tone: notify.Info, Subject: "rendimiento: test email",
		Title:   "Email notifications work",
		Summary: "This is how rendimiento will tell you about failed builds, rolled-back releases, outages and recoveries.",
		Facts: []notify.Fact{
			{Label: "Sent to", Value: strings.Join(s.Notify.To, ", ")},
			{Label: "Requested by", Value: login},
		},
		ActionURL: s.BaseURL + "/environment", ActionLabel: "Open rendimiento",
	})
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]any{"sent": true, "to": s.Notify.To})
}
