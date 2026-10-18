package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// The store is the wire of events.Bridge: Postgres LISTEN/NOTIFY between the
// platform's processes, and a table for events too large for a NOTIFY.

// Notify sends payload on a NOTIFY channel.
func (s *Store) Notify(ctx context.Context, channel, payload string) error {
	_, err := s.pool.Exec(ctx, `SELECT pg_notify($1, $2)`, channel, payload)
	return err
}

// Listen calls fn with each notification on channel, on a connection of its
// own, until ctx ends or the connection fails.
func (s *Store) Listen(ctx context.Context, channel string, fn func(payload string)) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	// The connection may be mid-wait when ctx ends: close it rather than
	// hand it back to the pool still listening.
	defer func() {
		_ = conn.Conn().Close(context.Background())
		conn.Release()
	}()
	if _, err := conn.Exec(ctx, "LISTEN "+pgx.Identifier{channel}.Sanitize()); err != nil {
		return err
	}
	for {
		n, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}
		fn(n.Payload)
	}
}

// PutEvent stores a large event and returns its id.
func (s *Store) PutEvent(ctx context.Context, payload []byte) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO live_events (payload) VALUES ($1) RETURNING id`, string(payload)).Scan(&id)
	return id, err
}

// GetEvent reads a stored event.
func (s *Store) GetEvent(ctx context.Context, id int64) ([]byte, error) {
	var p string
	err := s.pool.QueryRow(ctx, `SELECT payload FROM live_events WHERE id = $1`, id).Scan(&p)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return []byte(p), err
}

// PruneEvents deletes stored events older than before.
func (s *Store) PruneEvents(ctx context.Context, before time.Time) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM live_events WHERE created_at < $1`, before)
	return err
}
