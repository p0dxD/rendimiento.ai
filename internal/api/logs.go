package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/p0dxD/rendimiento.ai/internal/logarchive"
	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// archivedLog reads a step log from the log archive, or explains why it
// cannot: expired by the retention rule, or the archive is unreachable.
func (s *Server) archivedLog(ctx context.Context, ref string) string {
	if s.Logs == nil {
		return fmt.Sprintf("rendimiento: this log was archived to %s, but the log archive is not configured on this platform (LOG_ARCHIVE_ENDPOINT).\n", ref)
	}
	text, err := s.Logs.Read(ctx, ref)
	switch {
	case errors.Is(err, logarchive.ErrExpired):
		return fmt.Sprintf("rendimiento: this log has expired: archived logs are kept %d days.\n", s.Logs.RetentionDays)
	case err != nil:
		s.Log.Warn("reading an archived log", "ref", ref, "err", err)
		return "rendimiento: this log is in the log archive, which cannot be reached right now: " + err.Error() + "\n"
	}
	return text
}

// releaseTaskLog serves the log of a release's post-deploy task.
func (s *Server) releaseTaskLog(w http.ResponseWriter, r *http.Request, _ string) {
	a := s.app(w, r)
	if a == nil {
		return
	}
	number, err := strconv.ParseInt(r.PathValue("number"), 10, 64)
	if err != nil {
		httpError(w, http.StatusBadRequest, "bad release number")
		return
	}
	log, err := s.Store.ReleaseTaskLog(r.Context(), a.ID, number, r.PathValue("task"))
	if errors.Is(err, store.ErrNotFound) {
		httpError(w, http.StatusNotFound, "no such task in this release")
		return
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	io.WriteString(w, log)
}
