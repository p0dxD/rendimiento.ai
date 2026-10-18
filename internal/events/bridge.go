package events

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"strconv"
	"sync/atomic"
	"time"
)

// Channel is the Postgres NOTIFY channel the platform's processes share
// their live events on.
const Channel = "rendimiento_events"

// maxInline is the largest message sent in the NOTIFY itself (Postgres
// allows 8000 bytes); a larger event is stored and only its id is sent.
const maxInline = 7000

// Wire is what the bridge needs from the database (the store).
type Wire interface {
	Notify(ctx context.Context, channel, payload string) error
	// Listen calls fn with each notification on channel until ctx ends or
	// the connection fails.
	Listen(ctx context.Context, channel string, fn func(payload string)) error
	PutEvent(ctx context.Context, payload []byte) (int64, error)
	GetEvent(ctx context.Context, id int64) ([]byte, error)
	PruneEvents(ctx context.Context, before time.Time) error
}

// Bridge joins the hubs of the platform's processes through Postgres, so a
// run's log lines reach the UI from whichever process runs it, and two
// versions overlapping during an update see the same events. Each process
// publishes on its own hub as before; the bridge sends those events out and
// delivers the other processes' events to its hub. A process never gets its
// own events back.
type Bridge struct {
	Hub  *Hub
	Wire Wire
	Log  *slog.Logger

	origin  string
	out     chan wireOut
	dropped atomic.Int64
}

type wireOut struct {
	topic string
	e     Event
}

// message is what travels on the channel: the event itself, or (when too
// large) the id of the row that holds it.
type message struct {
	Origin string          `json:"o"`
	Topic  string          `json:"t"`
	Type   string          `json:"y,omitempty"`
	Data   json.RawMessage `json:"d,omitempty"`
	Ref    int64           `json:"r,omitempty"`
}

// Run relays this process's events and delivers the others' until ctx ends.
func (b *Bridge) Run(ctx context.Context) {
	if b.Log == nil {
		b.Log = slog.Default()
	}
	var id [8]byte
	_, _ = rand.Read(id[:])
	b.origin = hex.EncodeToString(id[:])
	b.out = make(chan wireOut, 1024)
	b.Hub.SetRelay(func(topic string, e Event) {
		select {
		case b.out <- wireOut{topic, e}:
		default: // Postgres is behind: other processes' subscribers refetch on reconnect
			if b.dropped.Add(1)%100 == 1 {
				b.Log.Warn("live events: dropping events for other processes", "dropped", b.dropped.Load())
			}
		}
	})
	defer b.Hub.SetRelay(nil)
	go b.send(ctx)
	for ctx.Err() == nil {
		err := b.Wire.Listen(ctx, Channel, func(p string) { b.receive(ctx, p) })
		if ctx.Err() != nil {
			return
		}
		b.Log.Warn("live events: listening stopped; retrying", "err", err)
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
		}
	}
}

// Origin identifies this process on the channel (set by Run).
func (b *Bridge) Origin() string { return b.origin }

func (b *Bridge) send(ctx context.Context) {
	prune := time.NewTicker(time.Minute)
	defer prune.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-prune.C:
			// A stored event is only needed while it is being delivered.
			if err := b.Wire.PruneEvents(ctx, time.Now().Add(-10*time.Minute)); err != nil {
				b.Log.Warn("live events: pruning stored events", "err", err)
			}
		case o := <-b.out:
			if err := b.publish(ctx, o); err != nil {
				b.Log.Warn("live events: sending an event to other processes", "topic", o.topic, "err", err)
			}
		}
	}
}

func (b *Bridge) publish(ctx context.Context, o wireOut) error {
	data, err := json.Marshal(o.e.Data)
	if err != nil {
		return err
	}
	m := message{Origin: b.origin, Topic: o.topic, Type: o.e.Type, Data: data}
	payload, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if len(payload) > maxInline {
		id, err := b.Wire.PutEvent(ctx, payload)
		if err != nil {
			return err
		}
		ref, _ := json.Marshal(message{Origin: b.origin, Topic: o.topic, Ref: id})
		payload = ref
	}
	return b.Wire.Notify(ctx, Channel, string(payload))
}

func (b *Bridge) receive(ctx context.Context, payload string) {
	var m message
	if err := json.Unmarshal([]byte(payload), &m); err != nil || m.Origin == b.origin {
		return
	}
	if m.Ref != 0 {
		full, err := b.Wire.GetEvent(ctx, m.Ref)
		if err != nil {
			b.Log.Warn("live events: reading a stored event", "id", strconv.FormatInt(m.Ref, 10), "err", err)
			return
		}
		if err := json.Unmarshal(full, &m); err != nil {
			return
		}
	}
	b.Hub.Deliver(m.Topic, Event{Type: m.Type, Data: m.Data})
}
