package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLockQueueHasOneOwner(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	release, err := s.LockQueue(ctx, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	// A second process waits while the first owns the queue.
	wait, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	if _, err := s.LockQueue(wait, 50*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second owner: err = %v, want to wait until the deadline", err)
	}
	// It gets the queue once the first lets go.
	release()
	got := make(chan error, 1)
	go func() {
		r, err := s.LockQueue(ctx, 50*time.Millisecond)
		if err == nil {
			r()
		}
		got <- err
	}()
	select {
	case err := <-got:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the queue was not handed over after release")
	}
}
