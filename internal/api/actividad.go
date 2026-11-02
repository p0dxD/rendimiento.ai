package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// activityPoll is how often the activity stream looks for what happened.
var activityPoll = 4 * time.Second

// activityStream sends the organization's activity as server-sent events
// (event: activity) while the page is open: runs that start and end,
// releases, encargos that move. It reads the database, so a run or an
// encargo another replica or process handled shows up all the same.
func (s *Server) activityStream(w http.ResponseWriter, r *http.Request, _ string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	ctx, org := r.Context(), orgOf(r)
	since, last, err := s.Store.ActivityCursor(ctx)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no") // ingress-nginx: do not buffer
	io.WriteString(w, ": connected\n\n")
	flusher.Flush()
	tick := time.NewTicker(activityPoll)
	defer tick.Stop()
	quiet := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		items, now, last2, err := s.Store.ActivitySince(ctx, org, since, last)
		if err != nil {
			continue // the next tick tries again from the same cursor
		}
		since, last = now, last2
		for _, a := range items {
			b, _ := json.Marshal(a)
			fmt.Fprintf(w, "event: activity\ndata: %s\n\n", b)
		}
		if quiet++; len(items) > 0 || quiet >= 5 { // a comment every 20 s keeps proxies open
			if len(items) == 0 {
				io.WriteString(w, ": ping\n\n")
			}
			quiet = 0
			flusher.Flush()
		}
	}
}
