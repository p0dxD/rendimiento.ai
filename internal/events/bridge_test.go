package events

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeWire is Postgres in memory: every listener gets every notification.
type fakeWire struct {
	mu        sync.Mutex
	listeners []chan string
	rows      map[int64][]byte
	next      int64
	notified  []string
}

func (w *fakeWire) Notify(_ context.Context, _ string, payload string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.notified = append(w.notified, payload)
	for _, l := range w.listeners {
		l <- payload
	}
	return nil
}

func (w *fakeWire) Listen(ctx context.Context, _ string, fn func(string)) error {
	ch := make(chan string, 64)
	w.mu.Lock()
	w.listeners = append(w.listeners, ch)
	w.mu.Unlock()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case p := <-ch:
			fn(p)
		}
	}
}

func (w *fakeWire) PutEvent(_ context.Context, p []byte) (int64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.next++
	w.rows[w.next] = p
	return w.next, nil
}

func (w *fakeWire) GetEvent(_ context.Context, id int64) ([]byte, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if p, ok := w.rows[id]; ok {
		return p, nil
	}
	return nil, errors.New("not found")
}

func (w *fakeWire) PruneEvents(context.Context, time.Time) error { return nil }

func (w *fakeWire) listening() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.listeners)
}

func receive(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(2 * time.Second):
		t.Fatal("no event arrived")
		return Event{}
	}
}

// Two processes: what one publishes reaches the other's subscribers, large
// events through a stored row, and nobody gets an event twice.
func TestBridge(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wire := &fakeWire{rows: map[int64][]byte{}}
	a, b := NewHub(), NewHub()
	go (&Bridge{Hub: a, Wire: wire}).Run(ctx)
	go (&Bridge{Hub: b, Wire: wire}).Run(ctx)
	for wire.listening() < 2 {
		time.Sleep(10 * time.Millisecond)
	}

	inA, stopA := a.Subscribe("run/7")
	defer stopA()
	inB, stopB := b.Subscribe("run/7")
	defer stopB()

	a.Publish("run/7", Event{Type: "step", Data: map[string]any{"id": "web:test", "status": "running"}})
	if e := receive(t, inA); e.Type != "step" {
		t.Fatalf("local subscriber got %+v", e)
	}
	e := receive(t, inB)
	var step map[string]string
	if err := json.Unmarshal(e.Data.(json.RawMessage), &step); err != nil || e.Type != "step" || step["status"] != "running" {
		t.Fatalf("other process got %+v (%v)", e, err)
	}

	// A log chunk larger than a NOTIFY travels through a stored row.
	text := strings.Repeat("línea de registro\n", 600)
	b.Publish("run/7", Event{Type: "log", Data: map[string]string{"step": "web:build", "text": text}})
	receive(t, inB)
	e = receive(t, inA)
	var log map[string]string
	if err := json.Unmarshal(e.Data.(json.RawMessage), &log); err != nil || log["text"] != text {
		t.Fatalf("large event arrived as %d bytes (%v)", len(log["text"]), err)
	}
	wire.mu.Lock()
	rows, notified := len(wire.rows), append([]string(nil), wire.notified...)
	wire.mu.Unlock()
	if rows != 1 {
		t.Fatalf("stored rows = %d", rows)
	}
	for _, n := range notified {
		if len(n) > maxInline {
			t.Fatalf("a %d-byte NOTIFY was sent", len(n))
		}
	}

	// Nothing comes back a second time (no echo of a process's own events).
	select {
	case e := <-inA:
		t.Fatalf("unexpected event %+v", e)
	case e := <-inB:
		t.Fatalf("unexpected event %+v", e)
	case <-time.After(200 * time.Millisecond):
	}
}

// A question asked in one process is answered by the other, and errors and
// silence come back as such.
func TestAsk(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wire := &fakeWire{rows: map[int64][]byte{}}
	control, api := NewHub(), NewHub()
	go (&Bridge{Hub: control, Wire: wire}).Run(ctx)
	go (&Bridge{Hub: api, Wire: wire}).Run(ctx)
	for wire.listening() < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	control.Answer(ctx, "sum", func(_ context.Context, data json.RawMessage) (any, error) {
		var n []int
		if err := json.Unmarshal(data, &n); err != nil {
			return nil, err
		}
		if len(n) == 0 {
			return nil, errors.New("nothing to add")
		}
		return n[0] + n[1], nil
	})

	for _, hub := range []*Hub{api, control} { // from another process, and from its own
		var sum int
		actx, done := context.WithTimeout(ctx, 2*time.Second)
		if err := hub.Ask(actx, "sum", []int{2, 3}, &sum); err != nil || sum != 5 {
			t.Fatalf("sum = %d (%v)", sum, err)
		}
		done()
	}
	actx, done := context.WithTimeout(ctx, 2*time.Second)
	defer done()
	var re *RemoteError
	if err := api.Ask(actx, "sum", []int{}, nil); !errors.As(err, &re) || re.Message != "nothing to add" {
		t.Fatalf("error = %v", err)
	}
	actx, done2 := context.WithTimeout(ctx, 100*time.Millisecond)
	defer done2()
	if err := api.Ask(actx, "nobody", 1, nil); !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("unanswered = %v", err)
	}
}
