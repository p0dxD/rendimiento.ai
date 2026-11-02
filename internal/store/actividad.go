package store

import (
	"context"
	"time"
)

// Activity is one thing that just happened in an organization, for the
// notices people see wherever they are in the UI: a run started or ended,
// a release went live or was rolled back, an encargo moved.
type Activity struct {
	Kind    string    `json:"kind"`  // run, release, encargo
	Event   string    `json:"event"` // started, succeeded, failed, cancelled; live, rolledBack; created, taken, delivered, released, sent, reviewed, returned
	App     string    `json:"app,omitempty"`
	Branch  string    `json:"branch,omitempty"`
	Deploy  bool      `json:"deploy,omitempty"`
	Run     int64     `json:"run,omitempty"`
	Release int64     `json:"release,omitempty"`
	Encargo int64     `json:"encargo,omitempty"`
	Title   string    `json:"title,omitempty"`
	Who     string    `json:"who,omitempty"`
	At      time.Time `json:"at"`
}

// ActivityCursor is where a stream of activity starts: the database's time
// now and the last encargo event so far.
func (s *Store) ActivityCursor(ctx context.Context) (time.Time, int64, error) {
	var now time.Time
	var last int64
	err := s.pool.QueryRow(ctx, `SELECT now(), COALESCE((SELECT max(id) FROM encargo_log), 0)`).Scan(&now, &last)
	return now, last, err
}

// ActivitySince is what happened in an organization after the cursor, and
// the cursor to ask from next time. Times are the database's own, so a
// browser's or a server's wrong clock changes nothing.
func (s *Store) ActivitySince(ctx context.Context, orgID int64, since time.Time, lastLog int64) ([]Activity, time.Time, int64, error) {
	var now time.Time
	var top int64 // the last encargo event now, of any organization
	if err := s.pool.QueryRow(ctx, `SELECT now(), COALESCE((SELECT max(id) FROM encargo_log), 0)`).Scan(&now, &top); err != nil {
		return nil, since, lastLog, err
	}
	var out []Activity
	rows, err := s.pool.Query(ctx, `SELECT r.id, a.name, r.branch, r.deploy, r.status, r.started_at, r.finished_at FROM runs r JOIN apps a ON a.id = r.app_id
		WHERE a.org_id = $1 AND ((r.started_at > $2 AND r.started_at <= $3) OR (r.finished_at > $2 AND r.finished_at <= $3)) ORDER BY r.id`, orgOr(orgID), since, now)
	if err != nil {
		return nil, since, lastLog, err
	}
	for rows.Next() {
		var a Activity
		var status string
		var started, finished *time.Time
		if err := rows.Scan(&a.Run, &a.App, &a.Branch, &a.Deploy, &status, &started, &finished); err != nil {
			rows.Close()
			return nil, since, lastLog, err
		}
		a.Kind = "run"
		if started != nil && started.After(since) && !started.After(now) {
			b := a
			b.Event, b.At = "started", *started
			out = append(out, b)
		}
		if finished != nil && finished.After(since) && !finished.After(now) {
			a.Event, a.At = status, *finished
			out = append(out, a)
		}
	}
	rows.Close()
	rows, err = s.pool.Query(ctx, `SELECT rl.number, a.name, rl.rollback_of IS NOT NULL, rl.created_at FROM releases rl JOIN apps a ON a.id = rl.app_id
		WHERE a.org_id = $1 AND rl.created_at > $2 AND rl.created_at <= $3 ORDER BY rl.id`, orgOr(orgID), since, now)
	if err != nil {
		return nil, since, lastLog, err
	}
	for rows.Next() {
		a := Activity{Kind: "release", Event: "live"}
		var rollback bool
		if err := rows.Scan(&a.Release, &a.App, &rollback, &a.At); err != nil {
			rows.Close()
			return nil, since, lastLog, err
		}
		if rollback {
			a.Event = "rolledBack"
		}
		out = append(out, a)
	}
	rows.Close()
	rows, err = s.pool.Query(ctx, `SELECT l.id, l.encargo_id, e.title, e.app, l.what, l.who, l.at FROM encargo_log l JOIN encargos e ON e.id = l.encargo_id
		WHERE e.org_id = $1 AND l.id > $2 AND l.id <= $3 AND l.what IN ('created', 'taken', 'delivered', 'released', 'sent', 'reviewed', 'returned')
		ORDER BY l.id LIMIT 50`, orgOr(orgID), lastLog, top)
	if err != nil {
		return nil, since, lastLog, err
	}
	for rows.Next() {
		a := Activity{Kind: "encargo"}
		var id int64
		if err := rows.Scan(&id, &a.Encargo, &a.Title, &a.App, &a.Event, &a.Who, &a.At); err != nil {
			rows.Close()
			return nil, since, lastLog, err
		}
		lastLog = max(lastLog, id)
		out = append(out, a)
	}
	n := 0
	for _, a := range out {
		if a.Kind == "encargo" {
			n++
		}
	}
	rows.Close()
	// Up to top everything was read (other organizations' events too, which
	// are not this stream's), unless the page of 50 was full.
	if n < 50 {
		lastLog = top
	}
	return out, now, lastLog, nil
}
