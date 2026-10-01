package api

import (
	"context"
	"errors"
	"fmt"

	"github.com/p0dxD/rendimiento.ai/internal/logarchive"
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
