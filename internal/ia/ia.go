// Package ia is the AI gateway: every call the platform makes to a model
// goes through it. It picks the provider by organization (the local model,
// the organization's own key, or the platform's key within the operator's
// allowance), keeps each organization and the platform under their monthly
// caps (falling back to the local model when a cap is reached), and records
// every call with its tokens and cost.
package ia

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// Message is one turn of a conversation: role user or assistant.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Request is what to ask a model.
type Request struct {
	System   string
	Messages []Message
	// MaxTokens bounds the answer (default 1024, at most 4096): a call can
	// pass a cap only by one answer's worth.
	MaxTokens int
}

// Response is a model's answer and what it cost.
type Response struct {
	Text         string `json:"text"`
	Provider     string `json:"provider"` // who paid: local, own or platform
	Model        string `json:"model"`
	InputTokens  int64  `json:"inputTokens"`
	OutputTokens int64  `json:"outputTokens"`
	CostMicros   int64  `json:"costMicros"`
	// Note says why the call went to the local model instead (a cap was
	// reached, no key), if it did.
	Note string `json:"note,omitempty"`
}

// ErrNoModel is a call with nowhere to go: the chosen provider cannot be
// used and there is no local model.
var ErrNoModel = errors.New("no model is available: the local model is not set up and the chosen provider cannot be used")

// Store is what the gateway keeps and reads (the platform's store).
type Store interface {
	AISettingsOf(ctx context.Context, orgID int64) (store.AISettings, error)
	AISpent(ctx context.Context, orgID int64, now time.Time) (own, platform int64, err error)
	AIPlatformSpent(ctx context.Context, now time.Time) (int64, error)
	RecordAIUsage(ctx context.Context, u store.AIUsage) error
}

// Keys holds organizations' own API keys (never in the database).
type Keys interface {
	OrgKey(ctx context.Context, orgID int64) (string, error)
}

// Gateway is the AI gateway.
type Gateway struct {
	Store Store
	Keys  Keys
	// LocalURL is the local model's Ollama (empty: no local model), and
	// LocalModel its model.
	LocalURL, LocalModel string
	// PlatformKey is the platform's Anthropic key (empty: none), used by
	// organizations the operator gives an allowance; PlatformModel its
	// model, and PlatformCapMicros the platform's monthly cap across all.
	PlatformKey       string
	PlatformModel     string
	PlatformCapMicros int64
	// Prices per model; Default when nil.
	Prices map[string]Price
	HTTP   *http.Client
	// AnthropicURL replaces Anthropic's API address (tests).
	AnthropicURL string
	now          func() time.Time
}

// Default models.
const (
	DefaultLocalModel     = "qwen3.5:4b"
	DefaultAnthropicModel = "claude-haiku-5-5"
)

// Price is a model's price in millionths of a dollar per million tokens.
type Price struct{ In, Out int64 }

// DefaultPrices are Anthropic's list prices (October 2026); a model not
// listed is measured at zero, so set a price for it before giving it a cap.
var DefaultPrices = map[string]Price{
	"claude-haiku-5-5":  {In: 100_000, Out: 500_000},
	"claude-sonnet-5-5": {In: 2_000_000, Out: 10_000_000},
	"claude-opus-5-5":   {In: 5_000_000, Out: 25_000_000},
}

// cost is a call's price, rounded up to the next millionth of a dollar.
func (g *Gateway) cost(model string, in, out int64) int64 {
	prices := g.Prices
	if prices == nil {
		prices = DefaultPrices
	}
	p := prices[model]
	return int64(math.Ceil((float64(in)*float64(p.In) + float64(out)*float64(p.Out)) / 1e6))
}

func (g *Gateway) clock() time.Time {
	if g.now != nil {
		return g.now()
	}
	return time.Now()
}

func (g *Gateway) http() *http.Client {
	if g.HTTP != nil {
		return g.HTTP
	}
	return &http.Client{Timeout: 3 * time.Minute}
}

// target is where a call goes and who pays.
type target struct {
	provider string // local, own or platform
	client   modelClient
	model    string
}

// choose picks the organization's provider, or the local model with the
// reason when that one cannot be used (no key, a cap reached).
func (g *Gateway) choose(ctx context.Context, set store.AISettings) (target, string, error) {
	local := target{provider: store.AILocal, client: ollama{url: g.LocalURL, http: g.http()}, model: or(g.LocalModel, DefaultLocalModel)}
	spentOwn, spentPlatform, err := g.Store.AISpent(ctx, set.OrgID, g.clock())
	if err != nil {
		return target{}, "", err
	}
	switch set.Provider {
	case store.AIOwn:
		key, err := g.Keys.OrgKey(ctx, set.OrgID)
		switch {
		case err != nil || key == "":
			return local, "the organization's key is not set", nil
		case spentOwn >= set.CapMicros:
			return local, fmt.Sprintf("the organization's monthly cap (%s) was reached", Dollars(set.CapMicros)), nil
		case set.Kind == "openai":
			return target{provider: store.AIOwn, client: openai{base: set.BaseURL, key: key, http: g.http()}, model: set.Model}, "", nil
		default:
			return target{provider: store.AIOwn, client: anthropic{url: g.AnthropicURL, key: key, http: g.http()}, model: or(set.Model, DefaultAnthropicModel)}, "", nil
		}
	case store.AIPlatform:
		limit := min(set.CapMicros, set.AllowanceMicros)
		total, err := g.Store.AIPlatformSpent(ctx, g.clock())
		switch {
		case err != nil:
			return target{}, "", err
		case g.PlatformKey == "":
			return local, "the platform has no AI key", nil
		case spentPlatform >= limit:
			return local, fmt.Sprintf("this organization's share of the platform's AI (%s this month) was reached", Dollars(limit)), nil
		case total >= g.PlatformCapMicros:
			return local, "the platform's monthly AI budget was reached", nil
		default:
			return target{provider: store.AIPlatform, client: anthropic{url: g.AnthropicURL, key: g.PlatformKey, http: g.http()}, model: or(g.PlatformModel, DefaultAnthropicModel)}, "", nil
		}
	}
	return local, "", nil
}

// Complete asks a model for an organization, from source (a feature, or an
// agent key's name), and records the call. When the organization's
// provider cannot be used, or fails, the local model answers and Note says
// why.
func (g *Gateway) Complete(ctx context.Context, orgID int64, source string, req Request) (Response, error) {
	return g.complete(ctx, orgID, source, "", req)
}

// CompleteVia asks a given provider for the organization (own or platform:
// an escalation to a stronger model) instead of the one it chose, with the
// same caps and the same fall back to the local model.
func (g *Gateway) CompleteVia(ctx context.Context, orgID int64, source, provider string, req Request) (Response, error) {
	return g.complete(ctx, orgID, source, provider, req)
}

func (g *Gateway) complete(ctx context.Context, orgID int64, source, provider string, req Request) (Response, error) {
	switch {
	case req.MaxTokens <= 0:
		req.MaxTokens = 1024
	case req.MaxTokens > 4096:
		req.MaxTokens = 4096
	}
	set, err := g.Store.AISettingsOf(ctx, orgID)
	if err != nil {
		return Response{}, err
	}
	if provider != "" {
		set.Provider = provider
	}
	t, note, err := g.choose(ctx, set)
	if err != nil {
		return Response{}, err
	}
	res, err := g.call(ctx, orgID, source, t, req, note)
	if err != nil && t.provider != store.AILocal {
		// The provider failed: the local model answers instead.
		local, _, _ := g.choose(ctx, store.AISettings{OrgID: orgID, Provider: store.AILocal})
		return g.call(ctx, orgID, source, local, req, "the "+t.provider+" model failed: "+err.Error())
	}
	return res, err
}

// call asks one target and records it.
func (g *Gateway) call(ctx context.Context, orgID int64, source string, t target, req Request, note string) (Response, error) {
	if t.provider == store.AILocal && g.LocalURL == "" {
		return Response{Note: note}, ErrNoModel
	}
	text, in, out, err := t.client.complete(ctx, t.model, req)
	res := Response{Text: text, Provider: t.provider, Model: t.model, InputTokens: in, OutputTokens: out, Note: note}
	if t.provider != store.AILocal {
		res.CostMicros = g.cost(t.model, in, out)
	}
	u := store.AIUsage{OrgID: orgID, Source: source, Provider: t.provider, Model: t.model, InputTokens: in, OutputTokens: out,
		CostMicros: res.CostMicros, OK: err == nil, Note: note}
	if err != nil {
		u.Note = err.Error()
	}
	if rerr := g.Store.RecordAIUsage(context.WithoutCancel(ctx), u); rerr != nil && err == nil {
		err = rerr
	}
	return res, err
}

// Dollars writes millionths of a dollar as dollars: $0.15, $5.00.
func Dollars(micros int64) string {
	return fmt.Sprintf("$%.2f", float64(micros)/1e6)
}

func or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
