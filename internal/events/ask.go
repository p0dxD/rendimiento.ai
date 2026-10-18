package events

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
)

// ErrNoAnswer is what Ask returns when nobody answered in time: the process
// that answers (the control service) is down or not elected yet.
var ErrNoAnswer = errors.New("the control service did not answer")

// request is an Ask: the data and the topic the answer goes to.
type request struct {
	Reply string          `json:"reply"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// answer is the reply to an Ask: data, or an error's message.
type answer struct {
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
}

// RemoteError is an error returned by the process that answered.
type RemoteError struct{ Message string }

func (e *RemoteError) Error() string { return e.Message }

// Ask publishes a request on topic and waits for an answer (see Answer)
// until ctx ends. Through the bridge, the answer may come from another
// process; in one process (ROLE=all), from this one. The answer is decoded
// into out when it is not nil.
func (h *Hub) Ask(ctx context.Context, topic string, data, out any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	var id [8]byte
	_, _ = rand.Read(id[:])
	reply := topic + "/answer/" + hex.EncodeToString(id[:])
	answers, stop := h.Subscribe(reply)
	defer stop()
	h.Publish(topic, Event{Type: "ask", Data: request{Reply: reply, Data: b}})
	select {
	case <-ctx.Done():
		return ErrNoAnswer
	case e := <-answers:
		var a answer
		if err := Decode(e.Data, &a); err != nil {
			return err
		}
		if a.Error != "" {
			return &RemoteError{a.Error}
		}
		if out == nil || len(a.Data) == 0 {
			return nil
		}
		return json.Unmarshal(a.Data, out)
	}
}

// Answer serves the requests published on topic with fn until ctx ends.
// Only one process should answer a topic (the control service's leader).
func (h *Hub) Answer(ctx context.Context, topic string, fn func(ctx context.Context, data json.RawMessage) (any, error)) {
	asks, stop := h.Subscribe(topic)
	go func() {
		<-ctx.Done()
		stop()
	}()
	go func() {
		for e := range asks {
			var r request
			if err := Decode(e.Data, &r); err != nil || r.Reply == "" {
				continue
			}
			go func() {
				var a answer
				out, err := fn(ctx, r.Data)
				if err != nil {
					a.Error = err.Error()
				} else if out != nil {
					a.Data, _ = json.Marshal(out)
				}
				h.Publish(r.Reply, Event{Type: "answer", Data: a})
			}()
		}
	}()
}

// Decode reads an event's data into v: a value published in this process,
// or JSON that came from another.
func Decode(data, v any) error {
	b, ok := data.(json.RawMessage)
	if !ok {
		var err error
		if b, err = json.Marshal(data); err != nil {
			return err
		}
	}
	return json.Unmarshal(b, v)
}
