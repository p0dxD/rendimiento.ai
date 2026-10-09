package platform

import (
	"context"
	"io"
	"strconv"
	"sync"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/events"
	"github.com/p0dxD/rendimiento.ai/internal/pipeline"
)

// StepUpdate implements pipeline.Recorder: persist, then notify the UI.
func (p *Platform) StepUpdate(runID, stepID string, r pipeline.StepResult) {
	id, _ := strconv.ParseInt(runID, 10, 64)
	if err := p.Store.UpdateStep(context.Background(), id, stepID, r); err != nil {
		p.Log.Error("update step", "run", runID, "step", stepID, "err", err)
	}
	p.Hub.Publish(RunTopic(id), events.Event{Type: "step", Data: map[string]any{
		"id": stepID, "status": r.Status, "digest": r.Digest, "message": r.Message,
	}})
}

// StepLog implements pipeline.Recorder. Lines reach the UI immediately;
// the database is written in batches to keep Postgres load low.
func (p *Platform) StepLog(runID, stepID string) io.WriteCloser {
	id, _ := strconv.ParseInt(runID, 10, 64)
	w := &logWriter{p: p, run: id, step: stepID, done: make(chan struct{}), stopped: make(chan struct{})}
	go w.flushLoop()
	return w
}

type logWriter struct {
	p    *Platform
	run  int64
	step string

	mu      sync.Mutex
	buf     []byte
	done    chan struct{}
	stopped chan struct{}
	closed  bool
}

// Write buffers step output and flushes it to the store and live subscribers.
func (w *logWriter) Write(b []byte) (int, error) {
	w.p.Hub.Publish(RunTopic(w.run), events.Event{Type: "log", Data: map[string]string{"step": w.step, "text": string(b)}})
	w.mu.Lock()
	w.buf = append(w.buf, b...)
	full := len(w.buf) >= 16<<10
	w.mu.Unlock()
	if full {
		w.flush()
	}
	return len(b), nil
}

// flush appends what was written since the last flush to the step's log
// in the database.
func (w *logWriter) flush() {
	w.mu.Lock()
	chunk := w.buf
	w.buf = nil
	w.mu.Unlock()
	if len(chunk) == 0 {
		return
	}
	if err := w.p.Store.AppendLog(context.Background(), w.run, w.step, string(chunk)); err != nil {
		w.p.Log.Warn("append log", "run", w.run, "step", w.step, "err", err)
	}
}

// flushLoop flushes every second until the step ends.
func (w *logWriter) flushLoop() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	defer close(w.stopped)
	for {
		select {
		case <-t.C:
			w.flush()
		case <-w.done:
			w.flush()
			return
		}
	}
}

// Close flushes what is left.
func (w *logWriter) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	w.mu.Unlock()
	close(w.done)
	<-w.stopped
	return nil
}
