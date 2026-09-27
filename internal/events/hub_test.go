package events

import "testing"

func TestHub(t *testing.T) {
	h := NewHub()
	a, cancelA := h.Subscribe("run/1")
	b, cancelB := h.Subscribe("run/2")
	defer cancelB()
	h.Publish("run/1", Event{Type: "log", Data: "x"})
	if e := <-a; e.Data != "x" {
		t.Fatal(e)
	}
	select {
	case e := <-b:
		t.Fatalf("wrong topic got %v", e)
	default:
	}
	cancelA()
	cancelA()                   // idempotent
	h.Publish("run/1", Event{}) // no subscribers, no panic
	for i := 0; i < 1000; i++ {
		h.Publish("run/2", Event{}) // never blocks
	}
}
