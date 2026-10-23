package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// AI providers an organization can choose (see internal/ia).
const (
	AILocal    = "local"    // the platform's local model: free
	AIOwn      = "own"      // the organization's own key: it pays its provider
	AIPlatform = "platform" // the platform's key, within the operator's allowance
)

// AISettings is an organization's choice of AI provider and its caps.
type AISettings struct {
	OrgID    int64  `json:"orgId"`
	Provider string `json:"provider"`
	// Kind is anthropic or openai (with provider own); BaseURL is an
	// OpenAI-compatible API's address.
	Kind    string `json:"kind,omitempty"`
	BaseURL string `json:"baseUrl,omitempty"`
	Model   string `json:"model,omitempty"`
	// CapMicros is the organization's monthly cap, in millionths of a
	// dollar; AllowanceMicros what the operator lets it spend of the
	// platform's key.
	CapMicros       int64 `json:"capMicros"`
	AllowanceMicros int64 `json:"allowanceMicros"`
	// AutoDiagnose: the diagnostician agent explains every failed run.
	AutoDiagnose bool `json:"autoDiagnose"`
	// EscalateTo is who answers new failures (nothing alike in the Ruta):
	// "" (nobody: the usual provider), own or platform; at most
	// EscalateLimit times a month.
	EscalateTo    string    `json:"escalateTo"`
	EscalateLimit int       `json:"escalateLimit"`
	UpdatedBy     string    `json:"updatedBy,omitempty"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// AIUsage is one measured call to a model.
type AIUsage struct {
	ID           int64     `json:"id"`
	OrgID        int64     `json:"orgId"`
	At           time.Time `json:"at"`
	Source       string    `json:"source"`
	Provider     string    `json:"provider"`
	Model        string    `json:"model"`
	InputTokens  int64     `json:"inputTokens"`
	OutputTokens int64     `json:"outputTokens"`
	CostMicros   int64     `json:"costMicros"`
	OK           bool      `json:"ok"`
	Note         string    `json:"note,omitempty"`
}

// AISettingsOf is an organization's AI settings (the defaults when it has
// none yet: the local model, a $1 cap, no allowance).
func (s *Store) AISettingsOf(ctx context.Context, orgID int64) (AISettings, error) {
	a := AISettings{OrgID: orgID}
	err := s.pool.QueryRow(ctx, `SELECT provider, kind, base_url, model, cap_micros, allowance_micros, auto_diagnose, escalate_to, escalate_limit, updated_by, updated_at
		FROM ai_settings WHERE org_id = $1`, orgID).Scan(&a.Provider, &a.Kind, &a.BaseURL, &a.Model, &a.CapMicros, &a.AllowanceMicros, &a.AutoDiagnose,
		&a.EscalateTo, &a.EscalateLimit, &a.UpdatedBy, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AISettings{OrgID: orgID, Provider: AILocal, CapMicros: 1_000_000, AutoDiagnose: true, EscalateLimit: 10}, nil
	}
	return a, err
}

// SaveAISettings stores an organization's choices (not the allowance, which
// is the operator's: see SetAIAllowance).
func (s *Store) SaveAISettings(ctx context.Context, a AISettings, by string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO ai_settings (org_id, provider, kind, base_url, model, cap_micros, updated_by, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now())
		ON CONFLICT (org_id) DO UPDATE SET provider = $2, kind = $3, base_url = $4, model = $5, cap_micros = $6, updated_by = $7, updated_at = now()`,
		a.OrgID, a.Provider, a.Kind, a.BaseURL, a.Model, a.CapMicros, by)
	return err
}

// SetAIAllowance is what the operator lets an organization spend of the
// platform's key each month.
func (s *Store) SetAIAllowance(ctx context.Context, orgID, micros int64, by string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO ai_settings (org_id, allowance_micros, updated_by, updated_at) VALUES ($1, $2, $3, now())
		ON CONFLICT (org_id) DO UPDATE SET allowance_micros = $2, updated_by = $3, updated_at = now()`, orgID, micros, by)
	return err
}

// RecordAIUsage keeps one call.
func (s *Store) RecordAIUsage(ctx context.Context, u AIUsage) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO ai_usage (org_id, source, provider, model, input_tokens, output_tokens, cost_micros, ok, note)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, u.OrgID, u.Source, u.Provider, u.Model, u.InputTokens, u.OutputTokens, u.CostMicros, u.OK, u.Note)
	return err
}

// monthStart is the first instant of now's month (UTC): caps are monthly.
func monthStart(now time.Time) time.Time {
	y, m, _ := now.UTC().Date()
	return time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
}

// AISpent is what an organization spent this month, by who paid: its own
// key, or the platform's.
func (s *Store) AISpent(ctx context.Context, orgID int64, now time.Time) (own, platform int64, err error) {
	err = s.pool.QueryRow(ctx, `SELECT COALESCE(sum(cost_micros) FILTER (WHERE provider = 'own'), 0),
		COALESCE(sum(cost_micros) FILTER (WHERE provider = 'platform'), 0)
		FROM ai_usage WHERE org_id = $1 AND at >= $2`, orgID, monthStart(now)).Scan(&own, &platform)
	return own, platform, err
}

// AIPlatformSpent is what every organization spent of the platform's key
// this month: the operator's bill.
func (s *Store) AIPlatformSpent(ctx context.Context, now time.Time) (int64, error) {
	var n int64
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(sum(cost_micros), 0) FROM ai_usage WHERE provider = 'platform' AND at >= $1`, monthStart(now)).Scan(&n)
	return n, err
}

// AIUsageOf lists an organization's latest calls.
func (s *Store) AIUsageOf(ctx context.Context, orgID int64, limit int) ([]AIUsage, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, org_id, at, source, provider, model, input_tokens, output_tokens, cost_micros, ok, note
		FROM ai_usage WHERE org_id = $1 ORDER BY at DESC LIMIT $2`, orgID, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (AIUsage, error) {
		var u AIUsage
		err := r.Scan(&u.ID, &u.OrgID, &u.At, &u.Source, &u.Provider, &u.Model, &u.InputTokens, &u.OutputTokens, &u.CostMicros, &u.OK, &u.Note)
		return u, err
	})
}

// SetEscalation is who answers an organization's new failures ("", own or
// platform), and how many times a month at most.
func (s *Store) SetEscalation(ctx context.Context, orgID int64, to string, limit int, by string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO ai_settings (org_id, escalate_to, escalate_limit, updated_by, updated_at) VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (org_id) DO UPDATE SET escalate_to = $2, escalate_limit = $3, updated_by = $4, updated_at = now()`, orgID, to, limit, by)
	return err
}

// AICalls counts an organization's calls from a source this month (the
// diagnostician's escalations, against their monthly limit).
func (s *Store) AICalls(ctx context.Context, orgID int64, source string, now time.Time) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM ai_usage WHERE org_id = $1 AND source = $2 AND at >= $3`, orgID, source, monthStart(now)).Scan(&n)
	return n, err
}
