package agentes

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/ia"
)

type slowModel struct {
	calls atomic.Int32
	asked ia.Request
	mu    sync.Mutex
}

func (m *slowModel) Complete(_ context.Context, _ int64, source string, req ia.Request) (ia.Response, error) {
	m.calls.Add(1)
	m.mu.Lock()
	m.asked = req
	m.mu.Unlock()
	time.Sleep(50 * time.Millisecond)
	if source != TranslateSource {
		return ia.Response{Text: "wrong source"}, nil
	}
	return ia.Response{Text: "  **Most likely cause:** the `hace` function.  "}, nil
}

// A diagnosis is translated keeping its Markdown and code; readers asking
// for the same translation at once share one call to the model.
func TestTranslate(t *testing.T) {
	m := &slowModel{}
	tr := &Translator{Model: m}
	var wg sync.WaitGroup
	got := make([]string, 3)
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i], _ = tr.Translate(context.Background(), 1, "diagnosis/7/1", "**Causa más probable:** la función `hace`.", "es", "en")
		}()
	}
	wg.Wait()
	for _, g := range got {
		if g != "**Most likely cause:** the `hace` function." {
			t.Fatalf("translation = %q", g)
		}
	}
	if n := m.calls.Load(); n != 1 {
		t.Errorf("%d calls to the model for one translation", n)
	}
	if !strings.Contains(m.asked.System, "Mexican Spanish") || !strings.Contains(m.asked.System, "to English") || !strings.Contains(m.asked.System, "Never translate what is inside code") {
		t.Errorf("instructions: %s", m.asked.System)
	}
	if _, err := tr.Translate(context.Background(), 1, "x", "hola", "es", "fr"); err == nil {
		t.Error("a language it does not know")
	}
}
