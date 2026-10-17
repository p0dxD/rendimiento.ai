package store

import (
	"context"
	"time"
)

// HistoryRun is a run as the Caminos tab draws it: the run, the steps that
// failed, and the release it made (0 if none).
type HistoryRun struct {
	Run
	Failed  []string `json:"failed,omitempty"`
	Release int64    `json:"release,omitempty"`
}

// RunHistory lists an app's runs created since a time, oldest first (at most
// 2,000), each with its failed steps and the release it made.
func (s *Store) RunHistory(ctx context.Context, appID int64, since time.Time) ([]HistoryRun, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.app_id, r.sha, r.branch, r.event, r.deploy, r.status, r.message, r.check_run,
		       r.created_at, r.started_at, r.finished_at,
		       COALESCE((SELECT array_agg(st.step_id ORDER BY st.step_id) FROM steps st WHERE st.run_id = r.id AND st.status = 'failed'), '{}'),
		       COALESCE((SELECT max(rel.number) FROM releases rel WHERE rel.run_id = r.id AND rel.rollback_of IS NULL), 0)
		FROM runs r WHERE r.app_id = $1 AND r.created_at >= $2 ORDER BY r.id LIMIT 2000`, appID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HistoryRun
	for rows.Next() {
		var h HistoryRun
		r := &h.Run
		if err := rows.Scan(&r.ID, &r.AppID, &r.SHA, &r.Branch, &r.Event, &r.Deploy, &r.Status, &r.Message, &r.CheckRun,
			&r.CreatedAt, &r.StartedAt, &r.FinishedAt, &h.Failed, &h.Release); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
