package ia

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/rendimiento-ai/rendimiento/internal/store"
)

// Tool is a tool a model may call: its name, what it does, and its input's
// JSON schema.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// Turn is one turn of a conversation with tools, as Anthropic content
// blocks: a model's turn is kept exactly as it came back (its thinking
// included), and the next user turn carries the tools' results.
type Turn struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// ToolCall is a model asking to run a tool.
type ToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ToolResult is what a tool answered, for the next user turn.
type ToolResult struct {
	Type      string `json:"type"` // tool_result
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error,omitempty"`
}

// ToolRequest is a turn of a conversation with tools. Model replaces the
// organization's model (the work needs a stronger one); MaxTokens bounds
// the answer (default 8,000, at most 16,000: a whole file may be written).
type ToolRequest struct {
	System    string
	Tools     []Tool
	Turns     []Turn
	Model     string
	MaxTokens int
}

// ToolResponse is the model's turn: its content to keep in the
// conversation, the text in it, the tools it asked for, and why it stopped
// (end_turn, tool_use, max_tokens, refusal).
type ToolResponse struct {
	Response
	Content json.RawMessage `json:"-"`
	Calls   []ToolCall      `json:"calls"`
	Stop    string          `json:"stop"`
}

// ErrNoToolModel is a conversation with tools for an organization whose
// model cannot have one: only Anthropic models do (its own key or the
// platform's), never the local model, which it does not fall back to.
var ErrNoToolModel = errors.New("work with tools needs an Anthropic model (your own key or the platform's)")

// CompleteTools asks the organization's Anthropic model one turn of a
// conversation with tools, within its caps, and records the call. Unlike
// Complete it never falls back to the local model: past a cap, or with no
// key, it fails with the reason.
func (g *Gateway) CompleteTools(ctx context.Context, orgID int64, source string, req ToolRequest) (ToolResponse, error) {
	switch {
	case req.MaxTokens <= 0:
		req.MaxTokens = 8000
	case req.MaxTokens > 16000:
		req.MaxTokens = 16000
	}
	set, err := g.Store.AISettingsOf(ctx, orgID)
	if err != nil {
		return ToolResponse{}, err
	}
	t, note, err := g.choose(ctx, set)
	if err != nil {
		return ToolResponse{}, err
	}
	a, ok := t.client.(anthropic)
	if !ok || t.provider == store.AILocal {
		if note != "" {
			return ToolResponse{}, fmt.Errorf("%w: %s", ErrNoToolModel, note)
		}
		return ToolResponse{}, ErrNoToolModel
	}
	a.http = &http.Client{Timeout: 10 * time.Minute} // a long answer takes minutes
	model := or(req.Model, t.model)
	res, u, err := a.tools(ctx, model, req)
	// A dropped connection or a busy API passes: try again a few times
	// before failing the turn (and, for the coding agent, the encargo).
	for try := 1; err != nil && passing(err) && try <= 3 && ctx.Err() == nil; try++ {
		select {
		case <-ctx.Done():
		case <-time.After(time.Duration(try*try) * retryPause):
		}
		res, u, err = a.tools(ctx, model, req)
	}
	res.Provider, res.Model = t.provider, model
	res.InputTokens, res.OutputTokens = u.In+u.CacheWrite+u.CacheRead, u.Out
	// Cached input costs a tenth to read and a quarter more to write.
	res.CostMicros = g.cost(model, int64(math.Ceil(float64(u.In)+1.25*float64(u.CacheWrite)+0.1*float64(u.CacheRead))), u.Out)
	rec := store.AIUsage{OrgID: orgID, Source: source, Provider: t.provider, Model: model, InputTokens: res.InputTokens,
		OutputTokens: u.Out, CostMicros: res.CostMicros, OK: err == nil}
	if err != nil {
		rec.Note = err.Error()
	}
	if rerr := g.Store.RecordAIUsage(context.WithoutCancel(ctx), rec); rerr != nil && err == nil {
		err = rerr
	}
	return res, err
}

// usage is what a call read and wrote, with what came from the cache.
type usage struct{ In, CacheWrite, CacheRead, Out int64 }

// tools is one turn with tools. The system prompt and the conversation so
// far are cached, so each turn pays a tenth for what it read before.
func (a anthropic) tools(ctx context.Context, model string, req ToolRequest) (ToolResponse, usage, error) {
	body := map[string]any{"model": model, "max_tokens": req.MaxTokens, "tools": req.Tools, "messages": req.Turns,
		"cache_control": map[string]string{"type": "ephemeral"}}
	if req.System != "" {
		body["system"] = []map[string]any{{"type": "text", "text": req.System, "cache_control": map[string]string{"type": "ephemeral"}}}
	}
	var out struct {
		Content    json.RawMessage `json:"content"`
		StopReason string          `json:"stop_reason"`
		Usage      struct {
			In         int64 `json:"input_tokens"`
			CacheWrite int64 `json:"cache_creation_input_tokens"`
			CacheRead  int64 `json:"cache_read_input_tokens"`
			Out        int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	err := post(ctx, a.http, strings.TrimRight(or(a.url, "https://api.anthropic.com"), "/")+"/v1/messages",
		map[string]string{"x-api-key": a.key, "anthropic-version": "2023-06-01"}, body, &out)
	u := usage{In: out.Usage.In, CacheWrite: out.Usage.CacheWrite, CacheRead: out.Usage.CacheRead, Out: out.Usage.Out}
	res := ToolResponse{Content: out.Content, Stop: out.StopReason}
	if err != nil {
		return res, u, err
	}
	var blocks []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(out.Content, &blocks); err != nil {
		return res, u, err
	}
	var text strings.Builder
	for _, b := range blocks {
		switch b.Type {
		case "text":
			text.WriteString(b.Text)
		case "tool_use":
			res.Calls = append(res.Calls, ToolCall{ID: b.ID, Name: b.Name, Input: b.Input})
		}
	}
	res.Text = text.String()
	return res, u, nil
}

// UserText is a user turn of plain text.
func UserText(text string) Turn {
	b, _ := json.Marshal([]map[string]string{{"type": "text", "text": text}})
	return Turn{Role: "user", Content: b}
}

// UserResults is a user turn with tools' results.
func UserResults(results []ToolResult) Turn {
	for i := range results {
		results[i].Type = "tool_result"
	}
	b, _ := json.Marshal(results)
	return Turn{Role: "user", Content: b}
}

// retryPause is the first pause before trying a turn again (then 4× and 9×).
var retryPause = 2 * time.Second

var busyStatus = regexp.MustCompile(`answered (429|5\d\d)`)

// passing says whether an error is one that goes away by itself: the
// connection dropped or timed out, or the API answered that it is busy
// (429, 5xx, 529 overloaded).
func passing(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET) {
		return true
	}
	s := err.Error()
	return busyStatus.MatchString(s) || strings.Contains(s, "connection reset") || strings.Contains(s, "EOF")
}
