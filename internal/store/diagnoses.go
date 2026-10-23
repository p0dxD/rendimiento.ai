package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Diagnosis states.
const (
	DiagnosisPending = "pending"
	DiagnosisDone    = "done"
	DiagnosisFailed  = "failed"
)

// Diagnosis is the diagnostician agent's answer for a failed run.
type Diagnosis struct {
	RunID      int64     `json:"runId"`
	Status     string    `json:"status"`
	Text       string    `json:"text,omitempty"`
	Provider   string    `json:"provider,omitempty"`
	Model      string    `json:"model,omitempty"`
	CostMicros int64     `json:"costMicros"`
	Note       string    `json:"note,omitempty"`
	AskedBy    string    `json:"askedBy,omitempty"`
	UpdatedAt  time.Time `json:"updatedAt"`
	// Signature is the failure's telling log line; Feedback what a person
	// said of the answer (useful, wrong), and RutaEntry the known failure
	// it became in the Ruta.
	Signature  string `json:"signature,omitempty"`
	Feedback   string `json:"feedback,omitempty"`
	FeedbackBy string `json:"feedbackBy,omitempty"`
	RutaEntry  *int64 `json:"rutaEntry,omitempty"`
}

// GetDiagnosis is a run's diagnosis (ErrNotFound when there is none).
func (s *Store) GetDiagnosis(ctx context.Context, runID int64) (Diagnosis, error) {
	d := Diagnosis{RunID: runID}
	err := s.pool.QueryRow(ctx, `SELECT status, text, provider, model, cost_micros, note, asked_by, updated_at, signature, feedback, feedback_by, ruta_entry
		FROM run_diagnoses WHERE run_id = $1`, runID).
		Scan(&d.Status, &d.Text, &d.Provider, &d.Model, &d.CostMicros, &d.Note, &d.AskedBy, &d.UpdatedAt, &d.Signature, &d.Feedback, &d.FeedbackBy, &d.RutaEntry)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, ErrNotFound
	}
	return d, err
}

// AskDiagnosis asks for a run's diagnosis (again): it is pending until the
// agent answers.
func (s *Store) AskDiagnosis(ctx context.Context, runID int64, by string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO run_diagnoses (run_id, status, asked_by) VALUES ($1, 'pending', $2)
		ON CONFLICT (run_id) DO UPDATE SET status = 'pending', text = '', note = '', asked_by = $2, updated_at = now()`, runID, by)
	return err
}

// SaveDiagnosis keeps the agent's answer.
func (s *Store) SaveDiagnosis(ctx context.Context, d Diagnosis) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO run_diagnoses (run_id, status, text, provider, model, cost_micros, note, asked_by, signature)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (run_id) DO UPDATE SET status = $2, text = $3, provider = $4, model = $5, cost_micros = $6, note = $7, signature = $9,
		  feedback = '', feedback_by = '', updated_at = now()`,
		d.RunID, d.Status, d.Text, d.Provider, d.Model, d.CostMicros, d.Note, d.AskedBy, d.Signature)
	return err
}

// RunsToDiagnose are the runs the agent should look at, oldest first:
// diagnoses asked for, and runs that failed since since in organizations
// with automatic diagnosis on, not diagnosed yet.
func (s *Store) RunsToDiagnose(ctx context.Context, since time.Time, limit int) ([]int64, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT run_id FROM (
			SELECT d.run_id, d.updated_at AS at FROM run_diagnoses d WHERE d.status = 'pending'
			UNION ALL
			SELECT r.id, r.finished_at FROM runs r
			  JOIN apps a ON a.id = r.app_id
			  LEFT JOIN ai_settings s ON s.org_id = a.org_id
			 WHERE r.status = 'failed' AND r.finished_at >= $1 AND COALESCE(s.auto_diagnose, true)
			   AND NOT EXISTS (SELECT 1 FROM run_diagnoses d WHERE d.run_id = r.id)
		) x ORDER BY at LIMIT $2`, since, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

// SetAutoDiagnose turns an organization's automatic diagnosis of failed
// runs on or off.
func (s *Store) SetAutoDiagnose(ctx context.Context, orgID int64, on bool, by string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO ai_settings (org_id, auto_diagnose, updated_by, updated_at) VALUES ($1, $2, $3, now())
		ON CONFLICT (org_id) DO UPDATE SET auto_diagnose = $2, updated_by = $3, updated_at = now()`, orgID, on, by)
	return err
}

// LastGoodSHA is the commit of an app's last successful run on a branch
// before a run (ErrNotFound when there is none): what changed since then
// likely broke it.
func (s *Store) LastGoodSHA(ctx context.Context, appID int64, branch string, before int64) (string, error) {
	var sha string
	err := s.pool.QueryRow(ctx, `SELECT sha FROM runs WHERE app_id = $1 AND branch = $2 AND status = 'succeeded' AND id < $3
		ORDER BY id DESC LIMIT 1`, appID, branch, before).Scan(&sha)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return sha, err
}

// SetDiagnosisFeedback keeps what a person said of a diagnosis, and the
// known failure it became in the Ruta.
func (s *Store) SetDiagnosisFeedback(ctx context.Context, runID int64, feedback, by string, entry int64) error {
	tag, err := s.pool.Exec(ctx, `UPDATE run_diagnoses SET feedback = $2, feedback_by = $3, ruta_entry = $4 WHERE run_id = $1`, runID, feedback, by, entry)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}
