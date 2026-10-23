package agentes

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/ia"
	"github.com/p0dxD/rendimiento.ai/internal/spec"
	"github.com/p0dxD/rendimiento.ai/internal/store"
)

type fakeStore struct {
	runs  map[int64]*store.Run
	saved map[int64]store.Diagnosis
}

func (f *fakeStore) RunsToDiagnose(context.Context, time.Time, int) ([]int64, error) {
	return []int64{7}, nil
}
func (f *fakeStore) GetRun(_ context.Context, id int64) (*store.Run, error) { return f.runs[id], nil }
func (f *fakeStore) GetAppByID(context.Context, int64) (*store.App, error) {
	return &store.App{ID: 1, Name: "shop", Repo: "o/shop", InstallationID: 9, OrgID: 3,
		Spec: spec.Spec{Services: []spec.Service{{Name: "web"}}}}, nil
}
func (f *fakeStore) GetDiagnosis(_ context.Context, id int64) (store.Diagnosis, error) {
	if d, ok := f.saved[id]; ok {
		return d, nil
	}
	return store.Diagnosis{}, store.ErrNotFound
}
func (f *fakeStore) SaveDiagnosis(_ context.Context, d store.Diagnosis) error {
	f.saved[d.RunID] = d
	return nil
}
func (f *fakeStore) LastGoodSHA(context.Context, int64, string, int64) (string, error) {
	return "goodsha1234", nil
}

type fakeModel struct {
	asked []ia.Request
	org   int64
	err   error
}

func (m *fakeModel) Complete(_ context.Context, org int64, source string, req ia.Request) (ia.Response, error) {
	m.asked, m.org = append(m.asked, req), org
	if source != Source {
		return ia.Response{}, fmt.Errorf("source %q", source)
	}
	return ia.Response{Text: "  Falla la prueba de precios.  ", Provider: store.AILocal, Model: "qwen"}, m.err
}

type changes struct{}

func (changes) ChangedFiles(context.Context, int64, string, string, string) ([]string, bool, error) {
	return []string{"api/precios.py"}, true, nil
}

// A failed run's diagnosis: the model reads the failed step and the end of
// its log (secret-looking lines left out) and the changed files, in the
// app's organization; its answer is kept. Without a model it says so.
func TestDiagnose(t *testing.T) {
	var log strings.Builder
	for i := 1; i <= 200; i++ {
		fmt.Fprintf(&log, "\x1b[31mlínea %d\x1b[0m\n", i)
	}
	log.WriteString("password: Abc123def456ghi789\nAssertionError: precio 0\n")
	st := &fakeStore{saved: map[int64]store.Diagnosis{}, runs: map[int64]*store.Run{
		7: {ID: 7, AppID: 1, Branch: "main", SHA: "badsha5678", Event: "push", Status: store.RunFailed,
			Steps: []store.Step{{ID: "web:build", Status: "succeeded"}, {ID: "web:test", Kind: "test", Status: "failed", Message: "exit 1"}}},
		8: {ID: 8, AppID: 1, Status: store.RunSucceeded},
	}}
	m := &fakeModel{}
	d := &Diagnostician{Store: st, Model: m, Changes: changes{}, Lang: "es", Log: slog.Default(),
		Logs: func(_ context.Context, run int64, step string) (string, error) {
			if step != "web:test" {
				t.Errorf("log of %s asked", step)
			}
			return log.String(), nil
		}}
	if err := d.Diagnose(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	got := st.saved[7]
	if got.Status != store.DiagnosisDone || got.Text != "Falla la prueba de precios." || got.Model != "qwen" || m.org != 3 {
		t.Fatalf("saved %+v, org %d", got, m.org)
	}
	ctx := m.asked[0].Messages[0].Content
	for _, want := range []string{"Failed step web:test (test): exit 1", "AssertionError: precio 0", "api/precios.py", "goodsha", "Services: web"} {
		if !strings.Contains(ctx, want) {
			t.Errorf("context misses %q:\n%s", want, ctx)
		}
	}
	for _, leak := range []string{"Abc123def456ghi789", "\x1b[", "línea 50\n"} {
		if strings.Contains(ctx, leak) {
			t.Errorf("context has %q", leak)
		}
	}
	if !strings.Contains(m.asked[0].System, "español") {
		t.Error("the answer's language")
	}

	m.err = ia.ErrNoModel
	if err := d.Diagnose(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if got := st.saved[7]; got.Status != store.DiagnosisFailed || !strings.Contains(got.Note, "no model") {
		t.Fatalf("without a model: %+v", got)
	}
	if err := d.Diagnose(context.Background(), 8); err != nil || st.saved[8].Status != store.DiagnosisFailed {
		t.Fatalf("a run that did not fail: %+v %v", st.saved[8], err)
	}
}
