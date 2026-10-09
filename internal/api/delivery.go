package api

import (
	"net/http"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// deliveryDays is the dashboard's period; the previous one, just as long,
// gives each number its trend.
const deliveryDays = 30

// DeliveryReport is the dashboard's delivery health: the last 30 days, the
// 30 before them, and twelve weeks of history.
type DeliveryReport struct {
	Days     int                  `json:"days"`
	TimeZone string               `json:"timeZone"`
	Current  *store.DeliveryStats `json:"current"`
	Previous *store.DeliveryStats `json:"previous"`
	Weekly   []store.DeliveryWeek `json:"weekly"` // oldest first
}

// delivery is the dashboard's delivery health: deploys, builds and their
// times, verifications, rollbacks and outages of the last 30 days next to
// the 30 before, and twelve weeks of history.
func (s *Server) delivery(w http.ResponseWriter, r *http.Request, _ string) {
	tzName := s.PublicTimeZone
	if tzName == "" {
		tzName = "UTC"
	}
	tz, err := time.LoadLocation(tzName)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	ctx, now := r.Context(), time.Now()
	out := DeliveryReport{Days: deliveryDays, TimeZone: tzName}
	if out.Current, err = s.Store.Delivery(ctx, deliveryDays, tzName); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if out.Previous, err = s.Store.DeliveryBetween(ctx, now.AddDate(0, 0, -2*deliveryDays), now.AddDate(0, 0, -deliveryDays)); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if out.Weekly, err = s.Store.DeliveryWeeks(ctx, 12, now, tz); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, out)
}
