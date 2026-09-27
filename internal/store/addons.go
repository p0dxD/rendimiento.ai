package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Addon is an optional platform feature and its settings (JSON, per add-on).
type Addon struct {
	Name          string          `json:"name"`
	Enabled       bool            `json:"enabled"`
	Settings      json.RawMessage `json:"settings"`
	LastScheduled *time.Time      `json:"lastScheduled,omitempty"`
	UpdatedAt     time.Time       `json:"updatedAt"`
}

// GetAddon returns the add-on's row, or ErrNotFound if it was never set up.
func (s *Store) GetAddon(ctx context.Context, name string) (*Addon, error) {
	var a Addon
	err := s.pool.QueryRow(ctx, `SELECT name, enabled, settings, last_scheduled, updated_at FROM addons WHERE name = $1`, name).
		Scan(&a.Name, &a.Enabled, &a.Settings, &a.LastScheduled, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &a, err
}

func (s *Store) PutAddon(ctx context.Context, name string, enabled bool, settings json.RawMessage) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO addons (name, enabled, settings) VALUES ($1, $2, $3)
		ON CONFLICT (name) DO UPDATE SET enabled = $2, settings = $3, updated_at = now()`, name, enabled, settings)
	return err
}

// ClaimSchedule records a scheduled start at `at` unless another worker
// already did (last_scheduled moved on). It returns whether this caller won.
func (s *Store) ClaimSchedule(ctx context.Context, name string, prev *time.Time, at time.Time) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE addons SET last_scheduled = $3
		WHERE name = $1 AND last_scheduled IS NOT DISTINCT FROM $2`, name, prev, at)
	return tag.RowsAffected() == 1, err
}

// AppAddons returns the app's per-add-on switches.
func (s *Store) AppAddons(ctx context.Context, appID int64) (map[string]bool, error) {
	rows, err := s.pool.Query(ctx, `SELECT addon, enabled FROM app_addons WHERE app_id = $1`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		var on bool
		if err := rows.Scan(&name, &on); err != nil {
			return nil, err
		}
		out[name] = on
	}
	return out, rows.Err()
}

func (s *Store) SetAppAddon(ctx context.Context, appID int64, addon string, enabled bool) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO app_addons (app_id, addon, enabled) VALUES ($1, $2, $3)
		ON CONFLICT (app_id, addon) DO UPDATE SET enabled = $3`, appID, addon, enabled)
	return err
}

// AppsWithAddon lists apps that have the add-on switched on.
func (s *Store) AppsWithAddon(ctx context.Context, addon string) ([]*App, error) {
	return s.listApps(ctx, `SELECT `+prefixed("a.", appCols)+` FROM apps a
		JOIN app_addons x ON x.app_id = a.id AND x.addon = $1 AND x.enabled ORDER BY a.name`, addon)
}

// AddonRun is one execution of an add-on (e.g. a Renovate run).
type AddonRun struct {
	ID         int64                      `json:"id"`
	Addon      string                     `json:"addon"`
	Trigger    string                     `json:"trigger"`
	Status     string                     `json:"status"` // running | succeeded | failed
	Message    string                     `json:"message"`
	Repos      []string                   `json:"repos"`
	Results    map[string]json.RawMessage `json:"results"`
	Log        string                     `json:"log,omitempty"`
	StartedAt  time.Time                  `json:"startedAt"`
	FinishedAt *time.Time                 `json:"finishedAt,omitempty"`
}

func (s *Store) CreateAddonRun(ctx context.Context, r *AddonRun) error {
	repos, _ := json.Marshal(r.Repos)
	return s.pool.QueryRow(ctx, `
		INSERT INTO addon_runs (addon, trigger, status, message, repos) VALUES ($1, $2, 'running', $3, $4)
		RETURNING id, status, started_at`, r.Addon, r.Trigger, r.Message, repos).Scan(&r.ID, &r.Status, &r.StartedAt)
}

func (s *Store) FinishAddonRun(ctx context.Context, id int64, status, message string, results map[string]json.RawMessage, log string) error {
	res, err := json.Marshal(results)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		UPDATE addon_runs SET status = $2, message = $3, results = $4, log = $5, finished_at = now()
		WHERE id = $1`, id, status, message, res, log)
	return err
}

// FailRunningAddonRuns closes runs a previous process left behind.
func (s *Store) FailRunningAddonRuns(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE addon_runs SET status = 'failed', message = 'interrupted: rendimiento restarted', finished_at = now() WHERE status = 'running'`)
	return tag.RowsAffected(), err
}

const addonRunCols = `id, addon, trigger, status, message, repos, results, started_at, finished_at`

func scanAddonRun(row pgx.Row, withLog bool) (*AddonRun, error) {
	var r AddonRun
	var repos, results []byte
	dest := []any{&r.ID, &r.Addon, &r.Trigger, &r.Status, &r.Message, &repos, &results, &r.StartedAt, &r.FinishedAt}
	if withLog {
		dest = append(dest, &r.Log)
	}
	if err := row.Scan(dest...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if err := json.Unmarshal(repos, &r.Repos); err != nil {
		return nil, err
	}
	return &r, json.Unmarshal(results, &r.Results)
}

func (s *Store) ListAddonRuns(ctx context.Context, addon string, limit int) ([]*AddonRun, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+addonRunCols+` FROM addon_runs WHERE addon = $1 ORDER BY id DESC LIMIT $2`, addon, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*AddonRun{}
	for rows.Next() {
		r, err := scanAddonRun(rows, false)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) GetAddonRun(ctx context.Context, addon string, id int64) (*AddonRun, error) {
	return scanAddonRun(s.pool.QueryRow(ctx, `SELECT `+addonRunCols+`, log FROM addon_runs WHERE addon = $1 AND id = $2`, addon, id), true)
}

// prefixed qualifies a column list with a table alias ("a.id, a.name").
func prefixed(p, cols string) string {
	parts := strings.Split(cols, ",")
	for i, c := range parts {
		parts[i] = p + strings.TrimSpace(c)
	}
	return strings.Join(parts, ", ")
}
