// Package traces keeps whole requests for the Universe's waterfall. It is
// the traces service (ROLE=traces): the OpenTelemetry Collector sends it
// every span over OTLP/HTTP, it waits until a request's spans have arrived,
// keeps the requests worth looking at (the ones that failed or were slow,
// and a sample of the rest), and stores them in Postgres in day partitions
// that are dropped whole after the retention.
package traces

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// Store is where kept requests go (the platform's store).
type Store interface {
	EnsureTracePartitions(ctx context.Context, now time.Time) error
	DropTracePartitions(ctx context.Context, before time.Time) (int, error)
	SaveTraces(ctx context.Context, traces []store.Trace, spans []store.Span) error
}

// Keeper buffers spans by request and decides which requests to keep.
type Keeper struct {
	Store Store
	Log   *slog.Logger
	// Sample is the share of normal requests kept (0.1: one in ten);
	// failed and slow ones are always kept.
	Sample float64
	// Slow is the duration from which a request is always kept.
	Slow time.Duration
	// Retention is how many days are kept.
	Retention int
	// Wait is how long a request's spans may keep arriving before the
	// decision (default 20s); MaxPending bounds the requests waiting
	// (default 20000: the oldest are decided early).
	Wait       time.Duration
	MaxPending int

	mu      sync.Mutex
	pending map[string]*pending
	decided map[string]decision // late spans follow their request's decision
}

type pending struct {
	first time.Time
	spans []store.Span
}

type decision struct {
	keep  bool
	until time.Time
}

// Add takes spans as they arrive (from the receiver).
func (k *Keeper) Add(spans []store.Span) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.pending == nil {
		k.pending, k.decided = map[string]*pending{}, map[string]decision{}
	}
	now := time.Now()
	for _, sp := range spans {
		if d, ok := k.decided[sp.TraceID]; ok {
			if d.keep { // a late span of a kept request: saved on the next pass
				p := k.pendingOf(sp.TraceID, now)
				p.spans = append(p.spans, sp)
			}
			continue
		}
		p := k.pendingOf(sp.TraceID, now)
		p.spans = append(p.spans, sp)
	}
}

func (k *Keeper) pendingOf(id string, now time.Time) *pending {
	p, ok := k.pending[id]
	if !ok {
		p = &pending{first: now}
		k.pending[id] = p
	}
	return p
}

// Run decides and saves every few seconds, and keeps the day partitions,
// until ctx ends.
func (k *Keeper) Run(ctx context.Context) {
	k.partitions(ctx)
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	hourly := time.NewTicker(time.Hour)
	defer hourly.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-hourly.C:
			k.partitions(ctx)
		case now := <-tick.C:
			k.Flush(ctx, now)
		}
	}
}

// partitions creates the days around now and drops the expired ones.
func (k *Keeper) partitions(ctx context.Context) {
	if err := k.Store.EnsureTracePartitions(ctx, time.Now()); err != nil {
		k.Log.Warn("traces: could not create the day partitions", "err", err)
	}
	days := max(k.Retention, 1)
	if n, err := k.Store.DropTracePartitions(ctx, time.Now().AddDate(0, 0, -days)); err != nil {
		k.Log.Warn("traces: could not drop expired days", "err", err)
	} else if n > 0 {
		k.Log.Info("traces: dropped expired days", "partitions", n)
	}
}

// Flush decides the requests that have waited long enough (or the oldest,
// when too many wait) and saves the kept ones.
func (k *Keeper) Flush(ctx context.Context, now time.Time) {
	k.mu.Lock()
	if k.Wait == 0 {
		k.Wait = 20 * time.Second
	}
	if k.MaxPending == 0 {
		k.MaxPending = 20000
	}
	if k.pending == nil {
		k.pending, k.decided = map[string]*pending{}, map[string]decision{}
	}
	type ready struct {
		id string
		p  *pending
	}
	var due []ready
	for id, p := range k.pending {
		if now.Sub(p.first) >= k.Wait {
			due = append(due, ready{id, p})
		}
	}
	if over := len(k.pending) - len(due) - k.MaxPending; over > 0 {
		var rest []ready
		for id, p := range k.pending {
			if now.Sub(p.first) < k.Wait {
				rest = append(rest, ready{id, p})
			}
		}
		slices.SortFunc(rest, func(a, b ready) int { return a.p.first.Compare(b.p.first) })
		due = append(due, rest[:over]...)
	}
	var traces []store.Trace
	var spans []store.Span
	for _, r := range due {
		delete(k.pending, r.id)
		if d, ok := k.decided[r.id]; ok { // late spans of a kept request
			if d.keep {
				spans = append(spans, r.p.spans...)
			}
			continue
		}
		t, keep := k.decide(r.id, r.p.spans)
		k.decided[r.id] = decision{keep: keep, until: now.Add(10 * time.Minute)}
		if keep {
			traces = append(traces, t)
			spans = append(spans, r.p.spans...)
		}
	}
	for id, d := range k.decided {
		if now.After(d.until) {
			delete(k.decided, id)
		}
	}
	k.mu.Unlock()
	if len(spans) == 0 {
		return
	}
	// Only days that have a partition (a clock far off would fail the batch).
	today := now.UTC().Truncate(24 * time.Hour)
	from, to := today.AddDate(0, 0, -1), today.AddDate(0, 0, 2)
	in := func(t time.Time) bool { return !t.Before(from) && t.Before(to) }
	traces = slices.DeleteFunc(traces, func(t store.Trace) bool { return !in(t.StartedAt) })
	spans = slices.DeleteFunc(spans, func(s store.Span) bool { return !in(s.StartedAt) })
	if err := k.Store.SaveTraces(ctx, traces, spans); err != nil {
		k.Log.Warn("traces: could not save requests", "requests", len(traces), "spans", len(spans), "err", err)
	}
}

// decide summarizes a request and says whether to keep it: always when it
// failed or was slow, otherwise for the sampled share (by its ID, so every
// span of it gets the same answer).
func (k *Keeper) decide(id string, spans []store.Span) (store.Trace, bool) {
	t := store.Trace{ID: id, Spans: len(spans)}
	ids := map[string]bool{}
	for _, sp := range spans {
		ids[sp.ID] = true
	}
	var end time.Time
	var root *store.Span
	for i := range spans {
		sp := &spans[i]
		if t.StartedAt.IsZero() || sp.StartedAt.Before(t.StartedAt) {
			t.StartedAt = sp.StartedAt
		}
		if e := sp.StartedAt.Add(time.Duration(sp.DurationUS) * time.Microsecond); e.After(end) {
			end = e
		}
		if sp.Error {
			t.Error = true
		}
		if !slices.Contains(t.Services, sp.Service) {
			t.Services = append(t.Services, sp.Service)
		}
		// The root: no parent, or a parent that never arrived; the earliest.
		if (sp.ParentID == "" || !ids[sp.ParentID]) && (root == nil || sp.StartedAt.Before(root.StartedAt)) {
			root = sp
		}
	}
	t.DurationUS = end.Sub(t.StartedAt).Microseconds()
	if root != nil {
		t.Service, t.Name = root.Service, root.Name
	}
	switch {
	case t.Error:
		t.Reason = "error"
	case k.Slow > 0 && time.Duration(t.DurationUS)*time.Microsecond >= k.Slow:
		t.Reason = "slow"
	case sampled(id, k.Sample):
		t.Reason = "sample"
	default:
		return t, false
	}
	return t, true
}

// sampled says whether a request falls in the sampled share, by the low
// bits of its (random) ID.
func sampled(id string, share float64) bool {
	if share >= 1 {
		return true
	}
	b, err := hex.DecodeString(id)
	if err != nil || len(b) < 8 || share <= 0 {
		return false
	}
	return float64(binary.BigEndian.Uint64(b[len(b)-8:])%10000) < share*10000
}
