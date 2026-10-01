package logarchive

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/store"
)

type memObjects struct{ m map[string][]byte }

func (o *memObjects) Put(_ context.Context, key string, data []byte) error {
	o.m[key] = data
	return nil
}
func (o *memObjects) Get(_ context.Context, key string) ([]byte, error) {
	d, ok := o.m[key]
	if !ok {
		return nil, ErrExpired
	}
	return d, nil
}

type memStore struct {
	steps    []store.ArchivableStep
	finished map[int64]time.Time
	archived map[string]string
	stuck    bool // MarkArchived never succeeds
}

func (s *memStore) UnarchivedSteps(_ context.Context, before time.Time, limit int) ([]store.ArchivableStep, error) {
	var out []store.ArchivableStep
	for _, st := range s.steps {
		if _, done := s.archived[st.StepID]; !done && s.finished[st.RunID].Before(before) && len(out) < limit {
			out = append(out, st)
		}
	}
	return out, nil
}
func (s *memStore) MarkArchived(_ context.Context, _ int64, stepID, ref string, _ int) (bool, error) {
	if s.stuck {
		return false, nil
	}
	s.archived[stepID] = ref
	return true, nil
}

// A sweep that cannot mark anything stops instead of uploading forever.
func TestSweepStopsWithoutProgress(t *testing.T) {
	st := &memStore{steps: []store.ArchivableStep{{RunID: 1, StepID: "a", App: "x", Log: "…"}},
		finished: map[int64]time.Time{1: time.Now().Add(-time.Hour)}, archived: map[string]string{}, stuck: true}
	a := &Archive{Objects: &memObjects{m: map[string][]byte{}}, Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	done := make(chan error, 1)
	go func() { _, err := a.Sweep(context.Background()); done <- err }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "none could be marked") {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the sweep looped instead of stopping")
	}
}

func TestSweepAndRead(t *testing.T) {
	big := strings.Repeat("building layer 3/9 …\n", 5000)
	st := &memStore{
		steps: []store.ArchivableStep{
			{RunID: 7, StepID: "web:build", App: "shop", Log: big},
			{RunID: 7, StepID: "web:test", App: "shop", Log: "ok\n"},
			{RunID: 8, StepID: "api:build", App: "shop", Log: "still running"},
		},
		finished: map[int64]time.Time{7: time.Now().Add(-time.Hour), 8: time.Now()}, // run 8 just finished
		archived: map[string]string{},
	}
	obj := &memObjects{m: map[string][]byte{}}
	a := &Archive{Objects: obj, Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), RetentionDays: 365}
	n, err := a.Sweep(context.Background())
	if err != nil || n != 2 {
		t.Fatalf("archived %d, %v (the run that finished just now must wait)", n, err)
	}
	key := st.archived["web:build"]
	if key != "runs/shop/7/web_build.log.gz" {
		t.Fatalf("key = %q", key)
	}
	if size := len(obj.m[key]); size == 0 || size > len(big)/20 {
		t.Fatalf("compressed %d bytes of %d: not compressed?", size, len(big))
	}
	text, err := a.Read(context.Background(), key)
	if err != nil || text != big {
		t.Fatalf("read back %d bytes, %v", len(text), err)
	}
	if _, err := a.Read(context.Background(), "runs/shop/1/gone.log.gz"); !errors.Is(err, ErrExpired) {
		t.Fatalf("missing object: %v", err)
	}
	if k := Key("my app", 3, "job-x:build/../y"); k != "runs/my_app/3/job-x_build_.._y.log.gz" {
		t.Fatalf("unsafe characters: %q", k)
	}
}
