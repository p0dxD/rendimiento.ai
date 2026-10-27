package store

import (
	"context"
	"time"
)

// Problem is one of the platform's own warnings or errors, with its repeats
// folded in.
type Problem struct {
	ID          int64      `json:"id"`
	Fingerprint string     `json:"-"`
	Level       string     `json:"level"`
	Component   string     `json:"component"`
	App         string     `json:"app"`
	Message     string     `json:"message"`
	Detail      string     `json:"detail"`
	Attrs       string     `json:"attrs"`
	Count       int        `json:"count"`
	FirstAt     time.Time  `json:"firstAt"`
	LastAt      time.Time  `json:"lastAt"`
	DismissedAt *time.Time `json:"dismissedAt,omitempty"`
	ResolvedAt  *time.Time `json:"resolvedAt,omitempty"`
	Resolution  string     `json:"resolution,omitempty"` // what fixed it, e.g. "fixed by c6eae54"
	// Days counts the occurrences per day (UTC, "2026-10-26"), for the last 31 days.
	Days map[string]int `json:"days,omitempty"`
}

// ProblemOpenFor is how long a problem counts as open after it last
// happened (unless dismissed or resolved).
const ProblemOpenFor = 24 * time.Hour

// RecordProblem adds an occurrence: a new row, or one more on the row with
// the same fingerprint, which also undoes a dismissal or resolution. The
// occurrence is also counted on its day, and days older than 31 are dropped.
func (s *Store) RecordProblem(ctx context.Context, p Problem) error {
	day := p.LastAt.UTC().Format("2006-01-02")
	since := p.LastAt.UTC().AddDate(0, 0, -31).Format("2006-01-02")
	_, err := s.pool.Exec(ctx, `
		INSERT INTO problems (fingerprint, level, component, app, message, detail, attrs, first_at, last_at, days)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8, jsonb_build_object($9::text, 1))
		ON CONFLICT (fingerprint) DO UPDATE SET
		  count = problems.count + 1, detail = EXCLUDED.detail, attrs = EXCLUDED.attrs,
		  last_at = GREATEST(problems.last_at, EXCLUDED.last_at), dismissed_at = NULL,
		  resolved_at = NULL, resolution = '',
		  days = (SELECT coalesce(jsonb_object_agg(k, v), '{}'::jsonb)
		          FROM jsonb_each(problems.days || jsonb_build_object($9::text, coalesce((problems.days->>$9::text)::int, 0) + 1)) AS d(k, v)
		          WHERE k >= $10)`,
		p.Fingerprint, p.Level, p.Component, p.App, p.Message, p.Detail, p.Attrs, p.LastAt, day, since)
	return err
}

// ListProblems returns problems that happened since `since`, newest first;
// app filters to one app ("" for all), and dismissed ones are left out
// unless asked for.
func (s *Store) ListProblems(ctx context.Context, app string, since time.Time, withDismissed bool, limit int) ([]Problem, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, level, component, app, message, detail, attrs, count, first_at, last_at, dismissed_at, resolved_at, resolution, days
		FROM problems
		WHERE last_at >= $1 AND ($2 = '' OR app = $2) AND ($3 OR dismissed_at IS NULL)
		ORDER BY last_at DESC LIMIT $4`, since, app, withDismissed, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Problem{}
	for rows.Next() {
		var p Problem
		if err := rows.Scan(&p.ID, &p.Level, &p.Component, &p.App, &p.Message, &p.Detail, &p.Attrs, &p.Count, &p.FirstAt, &p.LastAt, &p.DismissedAt, &p.ResolvedAt, &p.Resolution, &p.Days); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// OpenProblems counts problems that happened within ProblemOpenFor and are
// neither dismissed nor resolved.
func (s *Store) OpenProblems(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM problems WHERE dismissed_at IS NULL AND resolved_at IS NULL AND last_at >= $1`,
		time.Now().Add(-ProblemOpenFor)).Scan(&n)
	return n, err
}

// DismissProblem hides a problem until it happens again.
func (s *Store) DismissProblem(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `UPDATE problems SET dismissed_at = now() WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// PruneProblems deletes problems that last happened before `before`.
func (s *Store) PruneProblems(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM problems WHERE last_at < $1`, before)
	return tag.RowsAffected(), err
}

// ProblemMatch picks the problems a fix resolves: same message, and the
// same app (when set) and attribute (when set, e.g. "branch=main").
type ProblemMatch struct {
	Message string
	App     string
	Attr    string
}

// ResolveProblems marks the unresolved problems matching m as resolved,
// saying what fixed them; it returns how many it resolved.
func (s *Store) ResolveProblems(ctx context.Context, m ProblemMatch, resolution string) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE problems SET resolved_at = now(), resolution = $4
		WHERE resolved_at IS NULL AND message = $1 AND ($2 = '' OR app = $2)
		  AND ($3 = '' OR position(' ' || $3 || ' ' in ' ' || attrs || ' ') > 0)`,
		m.Message, m.App, m.Attr, resolution)
	return tag.RowsAffected(), err
}
