// Package events fans out live updates (step status, log lines) to UI
// subscribers over SSE. Slow subscribers drop messages rather than block CI.
package events

import "sync"

type Event struct {
	Type string `json:"type"` // step | log | run | app
	Data any    `json:"data"`
}

type Hub struct {
	mu   sync.Mutex
	subs map[string]map[chan Event]struct{}
}

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

func (h *Hub) Publish(topic string, e Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[topic] {
		select {
		case ch <- e:
		default: // subscriber is behind; it will refetch state on reconnect
		}
	}
}
