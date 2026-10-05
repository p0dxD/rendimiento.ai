package problems

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/p0dxD/rendimiento.ai/internal/store"
)

func queued(r *Recorder) []store.Problem {
	var out []store.Problem
	for {
		select {
		case p := <-r.ch:
			out = append(out, p)
		default:
			return out
		}
	}
}

// Warnings and errors are queued with their app, component and error;
// everything still reaches the real log.
func TestHandlerQueuesWarningsAndErrors(t *testing.T) {
	var buf bytes.Buffer
	r := NewRecorder()
	log := slog.New(r.Handler(slog.NewJSONHandler(&buf, nil)))

	log.Info("all good")
	log.With("component", "notify").Warn("notification email failed", "subject", "x", "err", errors.New("dial tcp 1.2.3.4:443: no route to host"))
	log.Error("could not queue", "app", "shop", "err", errors.New("bad yaml"))

	if strings.Count(buf.String(), "\n") != 3 {
		t.Fatalf("the real log lost records: %s", buf.String())
	}
	ps := queued(r)
	if len(ps) != 2 {
		t.Fatalf("queued %d, want 2: %+v", len(ps), ps)
	}
	if p := ps[0]; p.Level != "WARN" || p.Component != "notify" || p.Detail != "dial tcp 1.2.3.4:443: no route to host" || p.Attrs != "subject=x" {
		t.Fatalf("warning = %+v", p)
	}
	if p := ps[1]; p.Level != "ERROR" || p.App != "shop" || p.Detail != "bad yaml" || p.Fingerprint == "" {
		t.Fatalf("error = %+v", p)
	}
}

// Repeats that differ only in numbers or hashes share a fingerprint; a
// different app or message does not.
func TestFingerprintFoldsRepeats(t *testing.T) {
	r := NewRecorder()
	log := slog.New(r.Handler(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	log.Warn("rejected", "app", "shop", "err", errors.New("rendimiento.yaml at 0a5c611: services[0]: host used twice"))
	log.Warn("rejected", "app", "shop", "err", errors.New("rendimiento.yaml at c3a2ed8: services[1]: host used twice"))
	log.Warn("rejected", "app", "blog", "err", errors.New("rendimiento.yaml at c3a2ed8: services[1]: host used twice"))
	ps := queued(r)
	if ps[0].Fingerprint != ps[1].Fingerprint || ps[1].Fingerprint == ps[2].Fingerprint {
		t.Fatalf("fingerprints: %s %s %s", ps[0].Fingerprint, ps[1].Fingerprint, ps[2].Fingerprint)
	}
}

// Routine conflicts and outages (shown on the Reliability tab) are not problems.
func TestIgnoresRoutineRecords(t *testing.T) {
	r := NewRecorder()
	log := slog.New(r.Handler(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	log.Error("Reconciler error", "controller", "app", "err", errors.New(`Operation cannot be fulfilled on apps "x": the object has been modified; please apply your changes`))
	log.With("component", "uptime").Warn("service down", "app", "shop")
	log.Warn("sweep failed", "err", errors.New("context canceled"))
	if ps := queued(r); len(ps) != 0 {
		t.Fatalf("queued routine records: %+v", ps)
	}
}

// Fields inside a group keep the group's name; a full queue drops instead of blocking.
func TestGroupsAndFullQueue(t *testing.T) {
	r := &Recorder{ch: make(chan store.Problem, 1)}
	log := slog.New(r.Handler(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	log.WithGroup("req").Warn("slow", "path", "/x")
	log.Warn("second")
	ps := queued(r)
	if len(ps) != 1 || ps[0].Attrs != "req.path=/x" || r.dropped.Load() != 1 {
		t.Fatalf("queued %+v, dropped %d", ps, r.dropped.Load())
	}
}
