package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestEventsWire(t *testing.T) {
	s := open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	got := make(chan string, 4)
	lctx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- s.Listen(lctx, "rendimiento_test", func(p string) { got <- p }) }()
	// LISTEN runs asynchronously: notify until the listener has it.
	deadline := time.After(10 * time.Second)
	for received := false; !received; {
		if err := s.Notify(ctx, "rendimiento_test", "hola"); err != nil {
			t.Fatal(err)
		}
		select {
		case p := <-got:
			if p != "hola" {
				t.Fatalf("payload = %q", p)
			}
			received = true
		case <-time.After(200 * time.Millisecond):
		case <-deadline:
			t.Fatal("no notification arrived")
		}
	}
	stop()
	if err := <-done; err == nil {
		t.Fatal("Listen should return when its context ends")
	}

	big := []byte(`{"d":"` + strings.Repeat("x", 9000) + `"}`)
	id, err := s.PutEvent(ctx, big)
	if err != nil {
		t.Fatal(err)
	}
	back, err := s.GetEvent(ctx, id)
	if err != nil || string(back) != string(big) {
		t.Fatalf("stored event = %d bytes, %v", len(back), err)
	}
	if err := s.PruneEvents(ctx, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetEvent(ctx, id); err != ErrNotFound {
		t.Fatalf("pruned event: %v", err)
	}
}
