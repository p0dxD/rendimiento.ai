package agentes

import (
	"context"
	"log/slog"
	"testing"

	"github.com/rendimiento-ai/rendimiento/internal/ia"
	"github.com/rendimiento-ai/rendimiento/internal/store"
)

type estimateStore struct {
	auto  int64
	saved map[int64][]any
}

func (s *estimateStore) EncargosToEstimate(context.Context, int) ([]store.Encargo, error) {
	return nil, nil
}

func (s *estimateStore) SaveEstimate(_ context.Context, id int64, d int, micros int64, reason, route string) error {
	s.saved[id] = []any{d, micros, reason, route}
	return nil
}

func (s *estimateStore) AISettingsOf(_ context.Context, org int64) (store.AISettings, error) {
	return store.AISettings{OrgID: org, EncargoAutoMicros: s.auto}, nil
}

type estimateModel struct{ answers map[string]string }

func (m estimateModel) Complete(_ context.Context, _ int64, source string, req ia.Request) (ia.Response, error) {
	if source != EstimateSource {
		return ia.Response{}, nil
	}
	return ia.Response{Text: m.answers[req.Messages[0].Content]}, nil
}

func (estimateModel) Price(model string, in, out int64) int64 {
	return (&ia.Gateway{}).Price(model, in, out)
}

// An encargo under the organization's amount is for the platform's AI, one
// over it for the person's own agent; an answer with no difficulty is kept
// as such, so it is not asked again.
func TestEstimate(t *testing.T) {
	st := &estimateStore{auto: 100_000, saved: map[int64][]any{}}
	m := estimateModel{answers: map[string]string{
		"Title: un texto\n":  `{"difficulty": 2, "reason": "Cambiar un \"texto\"."}`,
		"Title: la cola\n":   "Pienso... ```json\n{\"difficulty\": 7, \"reason\": \"Toca la cola.\"}\n```",
		"Title: algo vago\n": "no sé",
	}}
	e := &Estimator{Store: st, Model: m, Lang: "es", Log: slog.Default()}
	for i, title := range []string{"un texto", "la cola", "algo vago"} {
		if err := e.Estimate(context.Background(), store.Encargo{ID: int64(i + 1), OrgID: 1, Title: title}); err != nil {
			t.Fatal(err)
		}
	}
	if got := st.saved[1]; got[0] != 2 || got[1] != int64(12_000) || got[2] != `Cambiar un "texto".` || got[3] != store.RoutePlatform {
		t.Errorf("difficulty 2 = %v, want $0.012 for the platform's AI", got)
	}
	if got := st.saved[2]; got[0] != 7 || got[1] != int64(147_000) || got[3] != store.RouteLocal {
		t.Errorf("difficulty 7 = %v, want $0.147, over the $0.10 amount: for the person's agent", got)
	}
	if got := st.saved[3]; got[0] != 0 || got[3] != "" {
		t.Errorf("no difficulty = %v, want it kept with no route", got)
	}
}
