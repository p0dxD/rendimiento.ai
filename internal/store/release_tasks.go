package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ReleaseTask is one post-deploy task of a release (see spec.Task).
type ReleaseTask struct {
	ReleaseID  int64      `json:"-"`
	Name       string     `json:"name"`
	Stage      string     `json:"stage"`  // pre-deploy or post-deploy
	Status     string     `json:"status"` // running, succeeded, failed, skipped
	Optional   bool       `json:"optional,omitempty"`
	Message    string     `json:"message,omitempty"`
	Log        string     `json:"-"` // served on its own
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

// Post-deploy task states.
const (
	TaskRunning   = "running"
	TaskSucceeded = "succeeded"
	TaskFailed    = "failed"
	TaskSkipped   = "skipped"
)

// maxTaskLog is how much of a task's log is kept: the tail, where errors are.
const maxTaskLog = 256 << 10

// SaveReleaseTask records a post-deploy task's state (and the tail of its log).
func (s *Store) SaveReleaseTask(ctx context.Context, t ReleaseTask) error {
	log := t.Log
	if len(log) > maxTaskLog {
		log = log[len(log)-maxTaskLog:]
	}
	stage := t.Stage
	if stage == "" {
		stage = "post-deploy"
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO release_tasks (release_id, name, status, optional, message, log, started_at, finished_at, stage)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (release_id, name) DO UPDATE SET status = $3, optional = $4, message = $5, log = $6, started_at = $7, finished_at = $8, stage = $9`,
		t.ReleaseID, t.Name, t.Status, t.Optional, t.Message, log, t.StartedAt, t.FinishedAt, stage)
	return err
}

// ReleaseTasks returns the post-deploy tasks of the given releases, by
// release ID, without their logs.
func (s *Store) ReleaseTasks(ctx context.Context, releaseIDs []int64) (map[int64][]ReleaseTask, error) {
	rows, err := s.pool.Query(ctx, `SELECT release_id, name, stage, status, optional, message, started_at, finished_at
		FROM release_tasks WHERE release_id = ANY($1) ORDER BY release_id, stage DESC, started_at NULLS LAST, name`, releaseIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]ReleaseTask{}
	for rows.Next() {
		var t ReleaseTask
		if err := rows.Scan(&t.ReleaseID, &t.Name, &t.Stage, &t.Status, &t.Optional, &t.Message, &t.StartedAt, &t.FinishedAt); err != nil {
			return nil, err
		}
		out[t.ReleaseID] = append(out[t.ReleaseID], t)
	}
	return out, rows.Err()
}

// ReleaseTaskLog returns the log of one post-deploy task of an app's release.
func (s *Store) ReleaseTaskLog(ctx context.Context, appID, number int64, name string) (string, error) {
	var log string
	err := s.pool.QueryRow(ctx, `SELECT t.log FROM release_tasks t JOIN releases r ON r.id = t.release_id
		WHERE r.app_id = $1 AND r.number = $2 AND t.name = $3`, appID, number, name).Scan(&log)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return log, err
}
