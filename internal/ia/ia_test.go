package ia

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/store"
)

type memStore struct {
	mu       sync.Mutex
	settings map[int64]store.AISettings
	usage    []store.AIUsage
}

func (m *memStore) AISettingsOf(_ context.Context, org int64) (store.AISettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.settings[org]; ok {
		return s, nil
	}
	return store.AISettings{OrgID: org, Provider: store.AILocal, CapMicros: 1_000_000}, nil
}
func (m *memStore) AISpent(_ context.Context, org int64, _ time.Time) (own, platform int64, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.usage {
		if u.OrgID == org && u.Provider == store.AIOwn {
			own += u.CostMicros
		}
		if u.OrgID == org && u.Provider == store.AIPlatform {
			platform += u.CostMicros
		}
	}
	return own, platform, nil
}
func (m *memStore) AIPlatformSpent(context.Context, time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for _, u := range m.usage {
		if u.Provider == store.AIPlatform {
			n += u.CostMicros
		}
	}
	return n, nil
}
func (m *memStore) RecordAIUsage(_ context.Context, u store.AIUsage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.usage = append(m.usage, u)
	return nil
}

type keys map[int64]string

func (k keys) OrgKey(_ context.Context, org int64) (string, error) { return k[org], nil }

// fakeModels answers like Ollama and like Anthropic; anthropicFails makes
// Anthropic answer 500.
func fakeModels(t *testing.T, anthropicFails *bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch r.URL.Path {
		case "/api/chat":
			_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"content": "local: " + body["model"].(string)},
				"prompt_eval_count": 50, "eval_count": 10})
		case "/v1/messages":
			if *anthropicFails {
				http.Error(w, `{"error":"overloaded"}`, http.StatusInternalServerError)
				return
			}
			if r.Header.Get("x-api-key") == "" || r.Header.Get("anthropic-version") == "" {
				http.Error(w, "no key", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"content": []map[string]any{{"type": "text", "text": "claude: " + r.Header.Get("x-api-key")}},
				"usage": map[string]any{"input_tokens": 20000, "output_tokens": 1500}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func ask(t *testing.T, g *Gateway, org int64) Response {
	t.Helper()
	res, err := g.Complete(context.Background(), org, "prueba", Request{Messages: []Message{{Role: "user", Content: "¿por qué falló?"}}})
	if err != nil {
		t.Fatalf("org %d: %v", org, err)
	}
	return res
}

// The local model by default; an organization's own key until its cap; the
// platform's key within the operator's allowance and the platform's cap;
// the local model whenever one of those cannot be used, with the reason.
func TestGateway(t *testing.T) {
	fails := false
	srv := fakeModels(t, &fails)
	st := &memStore{settings: map[int64]store.AISettings{
		2: {OrgID: 2, Provider: store.AIOwn, Kind: "anthropic", CapMicros: 5_000},
		3: {OrgID: 3, Provider: store.AIPlatform, CapMicros: 1_000_000, AllowanceMicros: 5_000},
		4: {OrgID: 4, Provider: store.AIPlatform, CapMicros: 1_000_000, AllowanceMicros: 0},
		5: {OrgID: 5, Provider: store.AIOwn, Kind: "anthropic", CapMicros: 1_000_000},
	}}
	g := &Gateway{Store: st, Keys: keys{2: "sk-de-la-org-2"}, LocalURL: srv.URL, AnthropicURL: srv.URL,
		PlatformKey: "sk-de-la-plataforma", PlatformCapMicros: 1_000_000}

	if res := ask(t, g, 1); res.Provider != store.AILocal || res.Text != "local: "+DefaultLocalModel || res.CostMicros != 0 {
		t.Fatalf("default: %+v", res)
	}
	// 20,000 in and 1,500 out on Haiku 5.5: 2,000 + 750 millionths.
	res := ask(t, g, 2)
	if res.Provider != store.AIOwn || res.Text != "claude: sk-de-la-org-2" || res.CostMicros != 2_750 {
		t.Fatalf("own key: %+v", res)
	}
	ask(t, g, 2) // 5,500 spent: over the 5,000 cap
	if res := ask(t, g, 2); res.Provider != store.AILocal || !strings.Contains(res.Note, "cap") {
		t.Fatalf("own key over its cap: %+v", res)
	}
	if res := ask(t, g, 5); res.Provider != store.AILocal || !strings.Contains(res.Note, "key is not set") {
		t.Fatalf("own key missing: %+v", res)
	}
	if res := ask(t, g, 3); res.Provider != store.AIPlatform || res.Text != "claude: sk-de-la-plataforma" {
		t.Fatalf("platform's key: %+v", res)
	}
	if res := ask(t, g, 4); res.Provider != store.AILocal || !strings.Contains(res.Note, "share") {
		t.Fatalf("no allowance: %+v", res)
	}
	g.PlatformCapMicros = 2_000 // the platform's budget is gone
	st.settings[3] = store.AISettings{OrgID: 3, Provider: store.AIPlatform, CapMicros: 1_000_000, AllowanceMicros: 1_000_000}
	if res := ask(t, g, 3); res.Provider != store.AILocal || !strings.Contains(res.Note, "budget") {
		t.Fatalf("platform's cap: %+v", res)
	}

	// A provider that fails: the local model answers, and both calls are kept.
	fails = true
	st.settings[2] = store.AISettings{OrgID: 2, Provider: store.AIOwn, Kind: "anthropic", CapMicros: 1_000_000}
	before := len(st.usage)
	if res := ask(t, g, 2); res.Provider != store.AILocal || !strings.Contains(res.Note, "failed") {
		t.Fatalf("failing provider: %+v", res)
	}
	if got := st.usage[before:]; len(got) != 2 || got[0].OK || !got[1].OK {
		t.Fatalf("recorded = %+v", got)
	}

	// Nowhere to go.
	g.LocalURL = ""
	if _, err := g.Complete(context.Background(), 1, "prueba", Request{Messages: []Message{{Role: "user", Content: "hola"}}}); !errors.Is(err, ErrNoModel) {
		t.Fatalf("no model: %v", err)
	}
}

func TestDollars(t *testing.T) {
	for micros, want := range map[int64]string{150_000: "$0.15", 5_000_000: "$5.00", 0: "$0.00"} {
		if got := Dollars(micros); got != want {
			t.Errorf("%d = %s", micros, got)
		}
	}
}

// Asking a given provider (an escalation) uses it within its caps, and
// falls back to the local model like any call.
func TestCompleteVia(t *testing.T) {
	fails := false
	srv := fakeModels(t, &fails)
	st := &memStore{settings: map[int64]store.AISettings{
		1: {OrgID: 1, Provider: store.AILocal, CapMicros: 1_000_000, AllowanceMicros: 1_000_000},
	}}
	g := &Gateway{Store: st, Keys: keys{1: "sk-propia"}, LocalURL: srv.URL, AnthropicURL: srv.URL, PlatformKey: "sk-plataforma", PlatformCapMicros: 1_000_000}
	req := Request{Messages: []Message{{Role: "user", Content: "¿por qué?"}}}
	if res, err := g.CompleteVia(context.Background(), 1, "x", store.AIPlatform, req); err != nil || res.Provider != store.AIPlatform || res.Text != "claude: sk-plataforma" {
		t.Fatalf("via the platform: %+v %v", res, err)
	}
	if res, _ := g.CompleteVia(context.Background(), 1, "x", store.AIOwn, req); res.Provider != store.AIOwn {
		t.Fatalf("via its own key: %+v", res)
	}
	if res, _ := g.Complete(context.Background(), 1, "x", req); res.Provider != store.AILocal {
		t.Fatalf("its usual provider stays the local one: %+v", res)
	}
}
