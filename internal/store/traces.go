package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Trace is one kept request: its root span and what it went through.
type Trace struct {
	ID         string    `json:"id"`
	StartedAt  time.Time `json:"startedAt"`
	DurationUS int64     `json:"durationUs"`
	Service    string    `json:"service"`
	Name       string    `json:"name"`
	Services   []string  `json:"services"`
	Spans      int       `json:"spans"`
	Error      bool      `json:"error"`
	// Reason is why it was kept: error, slow or sample.
	Reason string `json:"reason"`
}

// Span is one step of a trace.
type Span struct {
	TraceID       string         `json:"traceId"`
	ID            string         `json:"id"`
	ParentID      string         `json:"parentId,omitempty"`
	Service       string         `json:"service"`
	Name          string         `json:"name"`
	Kind          string         `json:"kind"`
	StartedAt     time.Time      `json:"startedAt"`
	DurationUS    int64          `json:"durationUs"`
	Error         bool           `json:"error"`
	StatusMessage string         `json:"statusMessage,omitempty"`
	Attributes    map[string]any `json:"attributes"`
	Events        []SpanEvent    `json:"events"`
}

// SpanEvent is something a span recorded at a moment (an exception, a log).
type SpanEvent struct {
	Name       string         `json:"name"`
	At         time.Time      `json:"at"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

// partition names a day's partition of a trace table: traces_20261018.
func partition(table string, day time.Time) string {
	return table + "_" + day.UTC().Format("20060102")
}

// EnsureTracePartitions creates the partitions of the days around now (UTC):
// yesterday, today and tomorrow.
func (s *Store) EnsureTracePartitions(ctx context.Context, now time.Time) error {
	today := now.UTC().Truncate(24 * time.Hour)
	for _, table := range []string{"traces", "spans"} {
		for d := -1; d <= 1; d++ {
			from := today.AddDate(0, 0, d)
			sql := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s PARTITION OF %s FOR VALUES FROM ('%s') TO ('%s')`,
				pgx.Identifier{partition(table, from)}.Sanitize(), table,
				from.Format(time.RFC3339), from.AddDate(0, 0, 1).Format(time.RFC3339))
			if _, err := s.pool.Exec(ctx, sql); err != nil {
				return fmt.Errorf("partition %s: %w", partition(table, from), err)
			}
		}
	}
	return nil
}

// DropTracePartitions drops the days before before (UTC) and says how many
// partitions went.
func (s *Store) DropTracePartitions(ctx context.Context, before time.Time) (int, error) {
	rows, err := s.pool.Query(ctx, `SELECT c.relname FROM pg_inherits i
		JOIN pg_class c ON c.oid = i.inhrelid JOIN pg_class p ON p.oid = i.inhparent
		WHERE p.relname IN ('traces', 'spans')`)
	if err != nil {
		return 0, err
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}
	limit := before.UTC().Truncate(24 * time.Hour)
	n := 0
	for _, name := range names {
		_, date, ok := strings.Cut(name, "_")
		day, err := time.Parse("20060102", date)
		if !ok || err != nil || !day.Before(limit) {
			continue
		}
		if _, err := s.pool.Exec(ctx, `DROP TABLE IF EXISTS `+pgx.Identifier{name}.Sanitize()); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// SaveTraces writes kept traces and their spans (spans of traces already
// saved may come later on their own).
func (s *Store) SaveTraces(ctx context.Context, traces []Trace, spans []Span) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if len(traces) > 0 {
		rows := make([][]any, len(traces))
		for i, t := range traces {
			rows[i] = []any{t.ID, t.StartedAt, t.DurationUS, t.Service, t.Name, t.Services, t.Spans, t.Error, t.Reason}
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"traces"},
			[]string{"trace_id", "started_at", "duration_us", "service", "name", "services", "spans", "error", "reason"}, pgx.CopyFromRows(rows)); err != nil {
			return err
		}
	}
	if len(spans) > 0 {
		rows := make([][]any, len(spans))
		for i, sp := range spans {
			attrs, _ := json.Marshal(orEmpty(sp.Attributes))
			events, _ := json.Marshal(sp.Events)
			if sp.Events == nil {
				events = []byte("[]")
			}
			rows[i] = []any{sp.TraceID, sp.ID, sp.ParentID, sp.Service, sp.Name, sp.Kind, sp.StartedAt, sp.DurationUS, sp.Error, sp.StatusMessage, string(attrs), string(events)}
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"spans"},
			[]string{"trace_id", "span_id", "parent_id", "service", "name", "kind", "started_at", "duration_us", "error", "status_message", "attributes", "events"},
			pgx.CopyFromRows(rows)); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// TraceQuery narrows ListTraces.
type TraceQuery struct {
	// Service is an app/service the request went through ("" for all).
	Service string
	// ErrorsOnly keeps the requests that failed somewhere.
	ErrorsOnly bool
	// Before pages back: requests that started before it.
	Before time.Time
	Limit  int
}

// ListTraces lists kept requests, newest first.
func (s *Store) ListTraces(ctx context.Context, q TraceQuery) ([]Trace, error) {
	if q.Limit <= 0 || q.Limit > 200 {
		q.Limit = 50
	}
	if q.Before.IsZero() {
		q.Before = time.Now().Add(time.Hour)
	}
	rows, err := s.pool.Query(ctx, `SELECT trace_id, started_at, duration_us, service, name, services, spans, error, reason
		FROM traces
		WHERE started_at < $1 AND ($2 = '' OR services @> ARRAY[$2]) AND (NOT $3 OR error)
		ORDER BY started_at DESC LIMIT $4`, q.Before, q.Service, q.ErrorsOnly, q.Limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Trace, error) {
		var t Trace
		err := r.Scan(&t.ID, &t.StartedAt, &t.DurationUS, &t.Service, &t.Name, &t.Services, &t.Spans, &t.Error, &t.Reason)
		return t, err
	})
}

// GetTrace returns a request's summary and its spans in start order
// (ErrNotFound when it was not kept or has expired).
func (s *Store) GetTrace(ctx context.Context, id string) (*Trace, []Span, error) {
	var t Trace
	err := s.pool.QueryRow(ctx, `SELECT trace_id, started_at, duration_us, service, name, services, spans, error, reason
		FROM traces WHERE trace_id = $1 ORDER BY started_at LIMIT 1`, id).
		Scan(&t.ID, &t.StartedAt, &t.DurationUS, &t.Service, &t.Name, &t.Services, &t.Spans, &t.Error, &t.Reason)
	if err == pgx.ErrNoRows {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT trace_id, span_id, parent_id, service, name, kind, started_at, duration_us, error, status_message, attributes, events
		FROM spans WHERE trace_id = $1 ORDER BY started_at, span_id`, id)
	if err != nil {
		return nil, nil, err
	}
	spans, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Span, error) {
		var sp Span
		var attrs, events []byte
		err := r.Scan(&sp.TraceID, &sp.ID, &sp.ParentID, &sp.Service, &sp.Name, &sp.Kind, &sp.StartedAt, &sp.DurationUS, &sp.Error, &sp.StatusMessage, &attrs, &events)
		if err == nil {
			_ = json.Unmarshal(attrs, &sp.Attributes)
			_ = json.Unmarshal(events, &sp.Events)
		}
		return sp, err
	})
	return &t, spans, err
}
