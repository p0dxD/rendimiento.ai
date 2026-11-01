package agentes

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/ia"
	"github.com/p0dxD/rendimiento.ai/internal/spec"
	"github.com/p0dxD/rendimiento.ai/internal/store"
)

type fakeStore struct {
	runs      map[int64]*store.Run
	saved     map[int64]store.Diagnosis
	ruta      []store.RutaEntry
	settings  store.AISettings
	escalated int
	links     map[int64]int64
}

func (f *fakeStore) AISettingsOf(context.Context, int64) (store.AISettings, error) {
	return f.settings, nil
}
func (f *fakeStore) AICalls(_ context.Context, _ int64, source string, _ time.Time) (int, error) {
	if source != Escalated {
		return 0, fmt.Errorf("counted %q", source)
	}
	return f.escalated, nil
}
func (f *fakeStore) AddRuta(_ context.Context, e store.RutaEntry) (store.RutaEntry, error) {
	e.ID = int64(len(f.ruta) + 100)
	f.ruta = append(f.ruta, e)
	return e, nil
}
func (f *fakeStore) LinkDiagnosisRuta(_ context.Context, run, entry int64) error {
	if f.links == nil {
		f.links = map[int64]int64{}
	}
	f.links[run] = entry
	return nil
}

func (f *fakeStore) SearchRuta(_ context.Context, _ int64, _, text string, _ int) ([]store.RutaEntry, error) {
	if text != KnownTag {
		return nil, fmt.Errorf("searched %q", text)
	}
	return f.ruta, nil
}

func (f *fakeStore) RunsToDiagnose(context.Context, time.Time, int) ([]int64, error) {
	return []int64{7}, nil
}
func (f *fakeStore) GetRun(_ context.Context, id int64) (*store.Run, error) { return f.runs[id], nil }
func (f *fakeStore) GetAppByID(context.Context, int64) (*store.App, error) {
	return &store.App{ID: 1, Name: "shop", Repo: "o/shop", InstallationID: 9, OrgID: 3, DefaultBranch: "main",
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
func (f *fakeStore) LastGoodSHA(_ context.Context, _ int64, branch string, _ int64) (string, error) {
	if branch != "main" {
		return "", store.ErrNotFound // a new branch: no good run of its own yet
	}
	return "goodsha1234", nil
}

type fakeModel struct {
	asked []ia.Request
	org   int64
	err   error
	via   []string
	empty bool // the stronger model spends its answer thinking
}

func (m *fakeModel) CompleteVia(_ context.Context, org int64, source, provider string, req ia.Request) (ia.Response, error) {
	m.asked, m.org, m.via = append(m.asked, req), org, append(m.via, provider)
	if source != Escalated {
		return ia.Response{}, fmt.Errorf("source %q", source)
	}
	if m.empty {
		return ia.Response{Provider: provider, Model: "claude-haiku-5-5", CostMicros: 1000}, nil
	}
	return ia.Response{Text: "Haiku dice: el etcd de la prueba tardó.", Provider: provider, Model: "claude-haiku-5-5", CostMicros: 3000}, nil
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
	for _, want := range []string{"Failed step web:test (test): exit 1", "AssertionError: precio 0", "api/precios.py", "last good run of main (goodsha", "Services: web"} {
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

	// A new branch has no good run yet: its changes are counted from the
	// default branch's last good run.
	st.runs[9] = &store.Run{ID: 9, AppID: 1, Branch: "descuentos", SHA: "newsha9999", Event: "push", Status: store.RunFailed,
		Steps: []store.Step{{ID: "web:test", Kind: "test", Status: "failed", Message: "exit 1"}}}
	if err := d.Diagnose(context.Background(), 9); err != nil {
		t.Fatal(err)
	}
	if ctx := m.asked[len(m.asked)-1].Messages[0].Content; !strings.Contains(ctx, "api/precios.py") || !strings.Contains(ctx, "last good run of main (goodsha") {
		t.Errorf("a new branch's context misses its changes:\n%s", ctx)
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

// The diagnostician keeps the failure's telling line, and passes the model
// the known failures of the Ruta that look alike (not the others).
func TestKnownFailures(t *testing.T) {
	st := &fakeStore{saved: map[int64]store.Diagnosis{}, runs: map[int64]*store.Run{
		7: {ID: 7, AppID: 1, Branch: "main", SHA: "badsha5678", Status: store.RunFailed,
			Steps: []store.Step{{ID: "platform:test", Kind: "test", Status: "failed", Message: "step exited with code 1"}}},
	}, ruta: []store.RutaEntry{
		{Title: "Falla conocida: etcdserver: request timed out", Body: "El etcd de envtest tarda en nodos ocupados: es transitorio; vuelva a correr.", Tags: []string{KnownTag, "rendimiento-ai"}},
		{Title: "Falla conocida: npm ERR! peer dependency", Body: "Fije la versión.", Tags: []string{KnownTag, "secplus"}},
	}}
	m := &fakeModel{}
	d := &Diagnostician{Store: st, Model: m, Lang: "es", Log: slog.Default(),
		Logs: func(context.Context, int64, string) (string, error) {
			return "=== RUN   TestAppLifecycle\n    app_controller_test.go:124: etcdserver: request timed out\n--- FAIL: TestAppLifecycle (16.55s)\nrendimiento: step failed\n", nil
		}}
	if err := d.Diagnose(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if sig := st.saved[7].Signature; sig != "app_controller_test.go:124: etcdserver: request timed out" {
		t.Fatalf("signature = %q", sig)
	}
	ctx := m.asked[0].Messages[0].Content
	if !strings.Contains(ctx, "vuelva a correr") || strings.Contains(ctx, "peer dependency") {
		t.Fatalf("known failures in the context:\n%s", ctx)
	}
}

// A new failure (nothing alike in the Ruta) goes to the stronger model the
// organization chose, at most its limit a month, and the answer becomes an
// unconfirmed known failure; a known one stays with the usual model.
func TestEscalation(t *testing.T) {
	run := &store.Run{ID: 7, AppID: 1, Branch: "main", SHA: "badsha5678", Status: store.RunFailed,
		Steps: []store.Step{{ID: "web:test", Kind: "test", Status: "failed", Message: "exit 1"}}}
	st := &fakeStore{saved: map[int64]store.Diagnosis{}, runs: map[int64]*store.Run{7: run},
		settings: store.AISettings{EscalateTo: store.AIPlatform, EscalateLimit: 2}}
	m := &fakeModel{}
	d := &Diagnostician{Store: st, Model: m, Lang: "es", Log: slog.Default(),
		Logs: func(context.Context, int64, string) (string, error) { return "etcdserver: request timed out\n", nil }}
	if err := d.Diagnose(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	got := st.saved[7]
	if len(m.via) != 1 || m.via[0] != store.AIPlatform || got.Model != "claude-haiku-5-5" || !strings.Contains(got.Note, "new failure") {
		t.Fatalf("escalated: via %v, saved %+v", m.via, got)
	}
	if len(st.ruta) != 1 || !slices.Contains(st.ruta[0].Tags, Unconfirmed) || !strings.Contains(st.ruta[0].Body, "Haiku dice") || st.links[7] != st.ruta[0].ID {
		t.Fatalf("remembered: %+v links %v", st.ruta, st.links)
	}

	// Now the Ruta knows it: the usual model answers.
	if err := d.Diagnose(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if len(m.via) != 1 || st.saved[7].Model != "qwen" {
		t.Fatalf("a known failure escalated: via %v, %+v", m.via, st.saved[7])
	}

	// Over the month's limit: no escalation.
	st.ruta, st.escalated = nil, 2
	if err := d.Diagnose(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if len(m.via) != 1 {
		t.Fatalf("escalated over the limit: %v", m.via)
	}

	// An answer with room to think, and an empty one is not an explanation
	// (nor remembered).
	st.ruta, st.escalated, m.empty = nil, 0, true
	if err := d.Diagnose(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if got := st.saved[7]; got.Status != store.DiagnosisFailed || !strings.Contains(got.Note, "thinking") || len(st.ruta) != 0 {
		t.Fatalf("empty answer: %+v, ruta %v", got, st.ruta)
	}
	if last := m.asked[len(m.asked)-1]; last.MaxTokens != AnswerTokens {
		t.Fatalf("max tokens = %d", last.MaxTokens)
	}
}
