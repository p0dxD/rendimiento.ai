package api

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/agentes"
	"github.com/p0dxD/rendimiento.ai/internal/ia"
	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// IAKeys stores organizations' own AI keys (ia.SecretKeys).
type IAKeys interface {
	KeyHint(ctx context.Context, orgID int64) (string, error)
	SetOrgKey(ctx context.Context, orgID int64, key string) error
}

// IAView is the organization's AI page.
type IAView struct {
	Settings store.AISettings `json:"settings"`
	// KeyHint is the end of the organization's own key ("" when none).
	KeyHint string `json:"keyHint,omitempty"`
	// What it spent this month, by who paid, and what each may reach.
	SpentOwnMicros      int64  `json:"spentOwnMicros"`
	SpentPlatformMicros int64  `json:"spentPlatformMicros"`
	LocalModel          string `json:"localModel,omitempty"` // "" when there is no local model
	PlatformAvailable   bool   `json:"platformAvailable"`
	PlatformModel       string `json:"platformModel,omitempty"`
	// Escalations is how many new failures went to a stronger model this
	// month (against Settings.EscalateLimit).
	Escalations int             `json:"escalations"`
	Usage       []store.AIUsage `json:"usage"`
}

// iaView is the organization's AI settings, spending and latest calls.
func (s *Server) iaView(w http.ResponseWriter, r *http.Request, _ string) {
	if s.IA == nil {
		httpError(w, http.StatusServiceUnavailable, "the AI gateway is not set up on this platform")
		return
	}
	ctx, org := r.Context(), orgOf(r)
	v := IAView{LocalModel: "", PlatformAvailable: s.IA.PlatformKey != ""}
	var err error
	if v.Settings, err = s.Store.AISettingsOf(ctx, org); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if s.IA.LocalURL != "" {
		v.LocalModel = s.IA.LocalModel
	}
	if v.PlatformAvailable {
		v.PlatformModel = s.IA.PlatformModel
	}
	if s.IAKeys != nil {
		v.KeyHint, _ = s.IAKeys.KeyHint(ctx, org)
	}
	if v.SpentOwnMicros, v.SpentPlatformMicros, err = s.Store.AISpent(ctx, org, time.Now()); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if v.Escalations, err = s.Store.AICalls(ctx, org, agentes.Escalated, time.Now()); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if v.Usage, err = s.Store.AIUsageOf(ctx, org, 30); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if v.Usage == nil {
		v.Usage = []store.AIUsage{}
	}
	writeJSON(w, v)
}

// IAInput is what an organization's administrator chooses.
type IAInput struct {
	Provider  string  `json:"provider"` // local, own, platform
	Kind      string  `json:"kind"`     // own: anthropic or openai
	BaseURL   string  `json:"baseUrl"`  // openai: the API's address (https)
	Model     string  `json:"model"`
	CapDollar float64 `json:"capDollars"` // monthly, 0 to 1000
	// Key replaces the organization's own key (empty: keep it); RemoveKey
	// deletes it.
	Key       string `json:"key"`
	RemoveKey bool   `json:"removeKey"`
	// AutoDiagnose turns the diagnostician's explanations of failed runs on
	// or off (nil: unchanged).
	AutoDiagnose *bool `json:"autoDiagnose"`
	// EscalateTo is who answers new failures ("", own or platform), at most
	// EscalateLimit times a month (nil: unchanged).
	EscalateTo    *string `json:"escalateTo"`
	EscalateLimit *int    `json:"escalateLimit"`
}

// micros turns dollars into millionths of a dollar.
func micros(d float64) int64 { return int64(math.Round(d * 1e6)) }

// putIA saves the organization's AI choices and, if given, its own key.
func (s *Server) putIA(w http.ResponseWriter, r *http.Request, login string) {
	if s.IA == nil {
		httpError(w, http.StatusServiceUnavailable, "the AI gateway is not set up on this platform")
		return
	}
	var in IAInput
	if !readJSON(w, r, &in) {
		return
	}
	in.BaseURL, in.Model, in.Key = strings.TrimSpace(in.BaseURL), strings.TrimSpace(in.Model), strings.TrimSpace(in.Key)
	switch {
	case in.Provider != store.AILocal && in.Provider != store.AIOwn && in.Provider != store.AIPlatform:
		httpError(w, http.StatusBadRequest, "provider must be local, own or platform")
		return
	case in.CapDollar < 0 || in.CapDollar > 1000:
		httpError(w, http.StatusBadRequest, "the monthly cap must be between $0 and $1,000")
		return
	case len(in.Model) > 100 || len(in.Key) > 500:
		httpError(w, http.StatusBadRequest, "the model or the key is too long")
		return
	}
	if in.Provider == store.AIOwn {
		switch in.Kind {
		case "anthropic":
			in.BaseURL = ""
		case "openai":
			u, err := url.Parse(in.BaseURL)
			if err != nil || u.Scheme != "https" || u.Host == "" || in.Model == "" {
				httpError(w, http.StatusBadRequest, "an OpenAI-compatible provider needs its https address and a model")
				return
			}
		default:
			httpError(w, http.StatusBadRequest, "the provider of your own key must be anthropic or openai")
			return
		}
	} else {
		in.Kind, in.BaseURL = "", ""
	}
	ctx, org := r.Context(), orgOf(r)
	if (in.Key != "" || in.RemoveKey) && s.IAKeys == nil {
		httpError(w, http.StatusServiceUnavailable, "this platform cannot keep keys")
		return
	}
	if in.RemoveKey {
		in.Key = ""
	}
	if in.Key != "" || in.RemoveKey {
		if err := s.IAKeys.SetOrgKey(ctx, org, in.Key); err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := s.Store.SaveAISettings(ctx, store.AISettings{OrgID: org, Provider: in.Provider, Kind: in.Kind, BaseURL: in.BaseURL,
		Model: in.Model, CapMicros: micros(in.CapDollar)}, login); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if in.EscalateTo != nil {
		limit := 10
		if in.EscalateLimit != nil {
			limit = *in.EscalateLimit
		}
		if (*in.EscalateTo != "" && *in.EscalateTo != store.AIOwn && *in.EscalateTo != store.AIPlatform) || limit < 0 || limit > 1000 {
			httpError(w, http.StatusBadRequest, "escalate to own or platform (or nobody), at most 1,000 times a month")
			return
		}
		if err := s.Store.SetEscalation(ctx, org, *in.EscalateTo, limit, login); err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if in.AutoDiagnose != nil {
		if err := s.Store.SetAutoDiagnose(ctx, org, *in.AutoDiagnose, login); err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	s.Log.Info("AI settings saved", "org", org, "provider", in.Provider, "kind", in.Kind, "keyChanged", in.Key != "" || in.RemoveKey, "by", login)
	s.iaView(w, r, login)
}

// testIA asks the organization's model one question, to try the settings
// (measured like any call).
func (s *Server) testIA(w http.ResponseWriter, r *http.Request, login string) {
	if s.IA == nil {
		httpError(w, http.StatusServiceUnavailable, "the AI gateway is not set up on this platform")
		return
	}
	var in struct {
		Prompt string `json:"prompt"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.Prompt = strings.TrimSpace(in.Prompt)
	if in.Prompt == "" || len(in.Prompt) > 2000 {
		httpError(w, http.StatusBadRequest, "ask something, up to 2,000 characters")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	res, err := s.IA.Complete(ctx, orgOf(r), "try: "+login, ia.Request{MaxTokens: 400,
		System:   "Answer briefly, in the language of the question.",
		Messages: []ia.Message{{Role: "user", Content: in.Prompt}}})
	if errors.Is(err, ia.ErrNoModel) {
		httpError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, res)
}

// IAOrg is one organization on the operator's AI page.
type IAOrg struct {
	Org                 store.Org        `json:"org"`
	Settings            store.AISettings `json:"settings"`
	SpentPlatformMicros int64            `json:"spentPlatformMicros"`
}

// iaOrgs is the operator's view: every organization's allowance of the
// platform's AI and what it spent of it this month, and the platform's bill.
func (s *Server) iaOrgs(w http.ResponseWriter, r *http.Request, _ string) {
	if s.IA == nil {
		httpError(w, http.StatusServiceUnavailable, "the AI gateway is not set up on this platform")
		return
	}
	ctx, now := r.Context(), time.Now()
	orgs, err := s.Store.AllOrgs(ctx)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := struct {
		Orgs           []IAOrg `json:"orgs"`
		SpentMicros    int64   `json:"spentMicros"`
		CapMicros      int64   `json:"capMicros"`
		PlatformKeySet bool    `json:"platformKeySet"`
	}{Orgs: []IAOrg{}, CapMicros: s.IA.PlatformCapMicros, PlatformKeySet: s.IA.PlatformKey != ""}
	if out.SpentMicros, err = s.Store.AIPlatformSpent(ctx, now); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, o := range orgs {
		set, err := s.Store.AISettingsOf(ctx, o.ID)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
		_, spent, err := s.Store.AISpent(ctx, o.ID, now)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out.Orgs = append(out.Orgs, IAOrg{Org: o, Settings: set, SpentPlatformMicros: spent})
	}
	writeJSON(w, out)
}

// putIAAllowance is what the operator lets an organization spend of the
// platform's AI each month.
func (s *Server) putIAAllowance(w http.ResponseWriter, r *http.Request, login string) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpError(w, http.StatusBadRequest, "bad organization id")
		return
	}
	var in struct {
		Dollars float64 `json:"dollars"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Dollars < 0 || in.Dollars > 1000 {
		httpError(w, http.StatusBadRequest, "the allowance must be between $0 and $1,000")
		return
	}
	if _, err := s.Store.GetOrg(r.Context(), id); err != nil {
		httpError(w, http.StatusNotFound, "no such organization")
		return
	}
	if err := s.Store.SetAIAllowance(r.Context(), id, micros(in.Dollars), login); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Log.Info("AI allowance set", "org", id, "dollars", in.Dollars, "by", login)
	w.WriteHeader(http.StatusNoContent)
}
