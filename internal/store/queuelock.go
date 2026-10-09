package store

import (
	"context"
	"time"
)

// queueLock is the advisory lock key of the run queue's owner (any number
// no other code uses; "rend" in ASCII).
const queueLock = 0x72656e64

// LockQueue waits until this process owns the run queue: a Postgres
// advisory lock, held on a connection of its own until release is called
// or the process dies (Postgres then lets it go). Only the owner recovers
// interrupted runs and works the queue, so a second copy of the platform
// (an overlapping pod, a binary started by hand) cannot put back in the
// queue runs the owner is still doing. It returns ctx's error if ctx ends
// first.
func (s *Store) LockQueue(ctx context.Context, every time.Duration) (release func(), err error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	for {
		var ok bool
		if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, queueLock).Scan(&ok); err != nil {
			conn.Release()
			return nil, err
		}
		if ok {
			return func() {
				_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, queueLock)
				conn.Release()
			}, nil
		}
		select {
		case <-ctx.Done():
			conn.Release()
			return nil, ctx.Err()
		case <-time.After(every):
		}
	}
}
