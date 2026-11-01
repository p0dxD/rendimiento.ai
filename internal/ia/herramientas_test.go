package ia

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// A turn with tools goes to the organization's Anthropic key with the tools,
// the conversation and caching; the model's turn comes back whole (thinking
// included) with the tools it asked for, priced with what came from the
// cache. The local model never answers it.
func TestCompleteTools(t *testing.T) {
	var asked map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&asked)
		_, _ = w.Write([]byte(`{"content":[{"type":"thinking","thinking":"","signature":"firma"},{"type":"text","text":"Leo el archivo."},
			{"type":"tool_use","id":"toolu_1","name":"read_file","input":{"path":"main.go"}}],"stop_reason":"tool_use",
			"usage":{"input_tokens":1000,"cache_creation_input_tokens":2000,"cache_read_input_tokens":100000,"output_tokens":500}}`))
	}))
	defer srv.Close()
	st := &memStore{settings: map[int64]store.AISettings{2: {OrgID: 2, Provider: store.AIOwn, Kind: "anthropic", CapMicros: 1_000_000}}}
	g := &Gateway{Store: st, Keys: keys{2: "sk-2"}, LocalURL: srv.URL, AnthropicURL: srv.URL}
	req := ToolRequest{System: "Usted programa.", Model: "claude-sonnet-5-5",
		Tools: []Tool{{Name: "read_file", Description: "lee", InputSchema: map[string]any{"type": "object"}}},
		Turns: []Turn{UserText("arregla main.go")}}
	res, err := g.CompleteTools(context.Background(), 2, "agent: coder", req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "Leo el archivo." || len(res.Calls) != 1 || res.Calls[0].Name != "read_file" || string(res.Calls[0].Input) != `{"path":"main.go"}` || res.Stop != "tool_use" {
		t.Fatalf("answer %+v", res)
	}
	if !strings.Contains(string(res.Content), `"signature":"firma"`) {
		t.Fatalf("the thinking was not kept: %s", res.Content)
	}
	// Sonnet 5.5 ($2 in, $10 out): 1,000 + 1.25·2,000 + 0.1·100,000 = 13,500 in, 500 out.
	if res.InputTokens != 103_000 || res.CostMicros != 32_000 || res.Provider != store.AIOwn {
		t.Fatalf("priced %+v", res.Response)
	}
	if asked["cache_control"] == nil || asked["model"] != "claude-sonnet-5-5" || len(asked["tools"].([]any)) != 1 {
		t.Fatalf("asked %v", asked)
	}
	if u := st.usage[len(st.usage)-1]; u.Source != "agent: coder" || u.CostMicros != 32_000 {
		t.Fatalf("recorded %+v", u)
	}
	if _, err := g.CompleteTools(context.Background(), 1, "agent: coder", req); !errors.Is(err, ErrNoToolModel) {
		t.Fatalf("the local model answered a turn with tools: %v", err)
	}
}
