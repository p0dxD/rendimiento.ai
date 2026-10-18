// Package events fans out live updates (step status, log lines) to UI
// subscribers over SSE. Slow subscribers drop messages rather than block CI.
package events

import "sync"

// Event is one live update sent to UI subscribers.
type Event struct {
	Type string `json:"type"` // step | log | run | app
	Data any    `json:"data"`
}

// Hub is an in-process publish/subscribe fan-out keyed by topic ("run/42",
// "app/shop"). With a relay (see Bridge), what is published here also reaches
// the hubs of the platform's other processes.
type Hub struct {
	mu    sync.Mutex
	subs  map[string]map[chan Event]struct{}
	relay func(topic string, e Event)
}

// NewHub returns an empty hub.
func NewHub() *Hub { return &Hub{subs: map[string]map[chan Event]struct{}{}} }

// Subscribe returns a channel for topic and a cancel func that must be called.
func (h *Hub) Subscribe(topic string) (<-chan Event, func()) {
	ch := make(chan Event, 256)
	h.mu.Lock()
	if h.subs[topic] == nil {
		h.subs[topic] = map[chan Event]struct{}{}
	}
	h.subs[topic][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		if _, ok := h.subs[topic][ch]; ok {
			delete(h.subs[topic], ch)
			close(ch)
		}
		if len(h.subs[topic]) == 0 {
			delete(h.subs, topic)
		}
		h.mu.Unlock()
	}
}

// SetRelay passes every event published here to relay as well (it must not
// block: CI publishes log lines).
func (h *Hub) SetRelay(relay func(topic string, e Event)) {
	h.mu.Lock()
	h.relay = relay
	h.mu.Unlock()
}

// Publish sends e to every subscriber of topic without blocking (a
// subscriber that is behind misses it and refetches on reconnect), and to
// the relay, if any.
func (h *Hub) Publish(topic string, e Event) {
	h.Deliver(topic, e)
	h.mu.Lock()
	relay := h.relay
	h.mu.Unlock()
	if relay != nil {
		relay(topic, e)
	}
}

// Deliver sends e to this process's subscribers only: an event that came
// from another process.
func (h *Hub) Deliver(topic string, e Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[topic] {
		select {
		case ch <- e:
		default: // subscriber is behind; it will refetch state on reconnect
		}
	}
}
