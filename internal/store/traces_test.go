package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Traces are kept in day partitions: saved, listed by service, read back
// with their spans, and gone once their day is dropped.
func TestTraces(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	now := time.Now()
	if err := s.EnsureTracePartitions(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureTracePartitions(ctx, now); err != nil { // again: no harm
		t.Fatal(err)
	}
	tr := Trace{ID: "t1", StartedAt: now, DurationUS: 1500, Service: "shop/web", Name: "GET /cart",
		Services: []string{"shop/web", "shop/api"}, Spans: 2, Error: true, Reason: "error"}
	spans := []Span{
		{TraceID: "t1", ID: "a", Service: "shop/web", Name: "GET /cart", Kind: "server", StartedAt: now, DurationUS: 1500},
		{TraceID: "t1", ID: "b", ParentID: "a", Service: "shop/api", Name: "SELECT carts", Kind: "client", StartedAt: now.Add(time.Millisecond),
			DurationUS: 400, Error: true, StatusMessage: "timeout", Attributes: map[string]any{"db.system": "postgresql"},
			Events: []SpanEvent{{Name: "exception", At: now.Add(time.Millisecond), Attributes: map[string]any{"exception.type": "Timeout"}}}},
	}
	if err := s.SaveTraces(ctx, []Trace{tr}, spans); err != nil {
		t.Fatal(err)
	}

	list, err := s.ListTraces(ctx, TraceQuery{Service: "shop/api"})
	if err != nil || len(list) != 1 || list[0].ID != "t1" || !list[0].Error {
		t.Fatalf("by service: %+v (%v)", list, err)
	}
	if list, _ := s.ListTraces(ctx, TraceQuery{Service: "other/x"}); len(list) != 0 {
		t.Fatalf("another service: %+v", list)
	}
	got, sp, err := s.GetTrace(ctx, "t1")
	if err != nil || got.Name != "GET /cart" || len(sp) != 2 || sp[1].ParentID != "a" || sp[1].Attributes["db.system"] != "postgresql" ||
		len(sp[1].Events) != 1 || sp[1].Events[0].Name != "exception" {
		t.Fatalf("trace %+v spans %+v (%v)", got, sp, err)
	}

	n, err := s.DropTracePartitions(ctx, now.AddDate(0, 0, 2))
	if err != nil || n != 6 {
		t.Fatalf("dropped %d (%v)", n, err)
	}
	if _, _, err := s.GetTrace(ctx, "t1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after dropping: %v", err)
	}
}
