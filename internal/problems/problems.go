// Package problems keeps the platform's own warnings and errors for the
// Problems page: a slog handler passes every record on to the real log and
// also queues warnings and errors; a Recorder stores them, folding repeats
// of the same problem into one row with a count.
package problems

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// Keep is how long a problem is kept after it last happened.
const Keep = 30 * 24 * time.Hour

// Recorder queues problems from its Handler and stores them in Run.
type Recorder struct {
	ch      chan store.Problem
	dropped atomic.Int64
}

// NewRecorder returns a Recorder that buffers up to 256 problems until Run
// starts storing them; beyond that, problems are counted and dropped rather
// than ever slowing the logging caller.
func NewRecorder() *Recorder { return &Recorder{ch: make(chan store.Problem, 256)} }

// Handler wraps h: every record still goes to h, and warnings and errors
// are also queued for the Problems page.
func (r *Recorder) Handler(h slog.Handler) slog.Handler { return &handler{inner: h, rec: r} }

// Run stores queued problems until ctx ends, and deletes the ones older
// than Keep every hour. log must not be built on Handler, so a failure to
// store a problem is never itself recorded as one.
func (r *Recorder) Run(ctx context.Context, st *store.Store, log *slog.Logger) {
	prune := func() {
		if _, err := st.PruneProblems(ctx, time.Now().Add(-Keep)); err != nil && ctx.Err() == nil {
			log.Warn("problems: could not delete old problems", "err", err)
		}
	}
	prune()
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			prune()
			if n := r.dropped.Swap(0); n > 0 {
				log.Warn("problems: some were not recorded (too many at once)", "dropped", n)
			}
		case p := <-r.ch:
			sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			if err := st.RecordProblem(sctx, p); err != nil && ctx.Err() == nil {
				log.Warn("problems: could not record", "message", p.Message, "err", err)
			}
			cancel()
		}
	}
}

func (r *Recorder) add(p store.Problem) {
	select {
	case r.ch <- p:
	default:
		r.dropped.Add(1)
	}
}

type handler struct {
	inner  slog.Handler
	rec    *Recorder
	attrs  []slog.Attr // from WithAttrs, keys already prefixed with their group
	prefix string      // the current group, "a.b."
}

func (h *handler) Enabled(ctx context.Context, l slog.Level) bool { return h.inner.Enabled(ctx, l) }

func (h *handler) WithAttrs(as []slog.Attr) slog.Handler {
	c := *h
	c.inner = h.inner.WithAttrs(as)
	c.attrs = append(append([]slog.Attr{}, h.attrs...), prefixed(h.prefix, as)...)
	return &c
}

func (h *handler) WithGroup(name string) slog.Handler {
	c := *h
	c.inner = h.inner.WithGroup(name)
	c.prefix = h.prefix + name + "."
	return &c
}

func (h *handler) Handle(ctx context.Context, rec slog.Record) error {
	err := h.inner.Handle(ctx, rec)
	if rec.Level >= slog.LevelWarn {
		all := append([]slog.Attr{}, h.attrs...)
		rec.Attrs(func(a slog.Attr) bool {
			all = append(all, prefixed(h.prefix, []slog.Attr{a})...)
			return true
		})
		if p, ok := FromRecord(rec.Level, rec.Message, rec.Time, all); ok {
			h.rec.add(p)
		}
	}
	return err
}

func prefixed(prefix string, as []slog.Attr) []slog.Attr {
	if prefix == "" {
		return as
	}
	out := make([]slog.Attr, len(as))
	for i, a := range as {
		out[i] = slog.Attr{Key: prefix + a.Key, Value: a.Value}
	}
	return out
}

// FromRecord turns a warning or error into a Problem; ok is false for the
// routine ones that are not worth showing.
func FromRecord(level slog.Level, msg string, at time.Time, attrs []slog.Attr) (store.Problem, bool) {
	p := store.Problem{Level: "WARN", Message: msg, LastAt: at}
	if level >= slog.LevelError {
		p.Level = "ERROR"
	}
	if p.LastAt.IsZero() {
		p.LastAt = time.Now()
	}
	var rest []string
	for _, a := range attrs {
		v := a.Value.Resolve().String()
		switch a.Key {
		case "app":
			p.App = v
		case "component", "controller":
			if p.Component == "" {
				p.Component = v
			}
		case "err", "error":
			p.Detail = v
		default:
			if a.Value.Kind() != slog.KindGroup && a.Value.Kind() != slog.KindAny {
				rest = append(rest, a.Key+"="+v)
			}
		}
	}
	p.Attrs = clip(strings.Join(rest, " "), 500)
	p.Detail = clip(p.Detail, 2000)
	if ignored(p) {
		return p, false
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{p.Level, p.Component, p.App, p.Message, normalize(p.Detail)}, "\x00")))
	p.Fingerprint = hex.EncodeToString(sum[:])
	return p, true
}

// ignored are warnings and errors that are routine or shown elsewhere:
//   - optimistic-concurrency conflicts, which the controllers retry at once;
//   - services going down, which the Reliability tab already tracks as outages;
//   - work stopped because the platform is shutting down.
func ignored(p store.Problem) bool {
	switch {
	case strings.Contains(p.Detail, "the object has been modified"):
		return true
	case p.Component == "uptime" && p.Message == "service down":
		return true
	case p.Detail == context.Canceled.Error():
		return true
	}
	return false
}

// Numbers, hashes and UUIDs differ between repeats of the same problem.
var variable = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|\b[0-9a-f]{7,}\b|\d+`)

func normalize(s string) string { return variable.ReplaceAllString(s, "#") }

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("… (%d more bytes)", len(s)-n)
}
