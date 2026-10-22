package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Encargo states: pending (nobody has it), taken (an agent works on it),
// waiting (the agent waits on a run), delivered (handed over, to review),
// reviewed (the person accepted it), cancelled.
const (
	EncargoPending   = "pending"
	EncargoTaken     = "taken"
	EncargoWaiting   = "waiting"
	EncargoDelivered = "delivered"
	EncargoReviewed  = "reviewed"
	EncargoCancelled = "cancelled"
)

// ErrEncargoState is a change an encargo's state does not allow (taking one
// someone has, delivering one that is not yours).
var ErrEncargoState = errors.New("the encargo is not in a state that allows this")

// Encargo is work asked of agents.
type Encargo struct {
	ID         int64        `json:"id"`
	Title      string       `json:"title"`
	Body       string       `json:"body"`
	App        string       `json:"app,omitempty"`
	RunID      *int64       `json:"runId,omitempty"`
	Status     string       `json:"status"`
	CreatedBy  string       `json:"createdBy"`
	ByAgent    bool         `json:"byAgent"`
	KeyID      *int64       `json:"keyId,omitempty"`
	KeyName    string       `json:"keyName,omitempty"` // the agent that has it
	CargoID    *int64       `json:"cargoId,omitempty"`
	WaitingRun *int64       `json:"waitingRun,omitempty"`
	Result     string       `json:"result,omitempty"`
	Links      []string     `json:"links"`
	Comment    string       `json:"comment,omitempty"`
	CreatedAt  time.Time    `json:"createdAt"`
	UpdatedAt  time.Time    `json:"updatedAt"`
	Log        []EncargoLog `json:"log,omitempty"`
}

// EncargoLog is one thing that happened to an encargo.
type EncargoLog struct {
	At   time.Time `json:"at"`
	Who  string    `json:"who"`
	What string    `json:"what"`
	Note string    `json:"note,omitempty"`
}

const encargoCols = `e.id, e.title, e.body, e.app, e.run_id, e.status, e.created_by, e.by_agent, e.key_id, COALESCE(k.name, ''),
	e.cargo_id, e.waiting_run, e.result, e.links, e.comment, e.created_at, e.updated_at`

const encargoFrom = ` FROM encargos e LEFT JOIN agent_keys k ON k.id = e.key_id`

func scanEncargo(row pgx.Row) (Encargo, error) {
	var e Encargo
	err := row.Scan(&e.ID, &e.Title, &e.Body, &e.App, &e.RunID, &e.Status, &e.CreatedBy, &e.ByAgent, &e.KeyID, &e.KeyName,
		&e.CargoID, &e.WaitingRun, &e.Result, &e.Links, &e.Comment, &e.CreatedAt, &e.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return e, ErrNotFound
	}
	return e, err
}

// CreateEncargo adds an encargo, pending.
func (s *Store) CreateEncargo(ctx context.Context, e Encargo) (Encargo, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return e, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO encargos (title, body, app, run_id, created_by, by_agent) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		e.Title, e.Body, e.App, e.RunID, e.CreatedBy, e.ByAgent).Scan(&id); err != nil {
		return e, err
	}
	if err := logEncargo(ctx, tx, id, e.CreatedBy, "created", ""); err != nil {
		return e, err
	}
	if err := tx.Commit(ctx); err != nil {
		return e, err
	}
	return s.Encargo(ctx, id)
}

func logEncargo(ctx context.Context, tx pgx.Tx, id int64, who, what, note string) error {
	_, err := tx.Exec(ctx, `INSERT INTO encargo_log (encargo_id, who, what, note) VALUES ($1, $2, $3, $4)`, id, who, what, note)
	return err
}

// Encargo returns one encargo with its history.
func (s *Store) Encargo(ctx context.Context, id int64) (Encargo, error) {
	e, err := scanEncargo(s.pool.QueryRow(ctx, `SELECT `+encargoCols+encargoFrom+` WHERE e.id = $1`, id))
	if err != nil {
		return e, err
	}
	rows, err := s.pool.Query(ctx, `SELECT at, who, what, note FROM encargo_log WHERE encargo_id = $1 ORDER BY at, id`, id)
	if err != nil {
		return e, err
	}
	e.Log, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (EncargoLog, error) {
		var l EncargoLog
		err := r.Scan(&l.At, &l.Who, &l.What, &l.Note)
		return l, err
	})
	return e, err
}

// ListEncargos lists encargos, the most recently changed first: the open
// ones (not reviewed or cancelled), or all.
func (s *Store) ListEncargos(ctx context.Context, openOnly bool, limit int) ([]Encargo, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `SELECT `+encargoCols+encargoFrom+`
		WHERE NOT $1 OR e.status NOT IN ('reviewed', 'cancelled') ORDER BY e.updated_at DESC LIMIT $2`, openOnly, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Encargo, error) { return scanEncargo(r) })
}

// change applies one state change in a transaction: the update must touch
// the row (else ErrEncargoState), and it is logged.
func (s *Store) change(ctx context.Context, id int64, who, what, note, sql string, args ...any) (Encargo, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Encargo{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, sql, args...)
	if err != nil {
		return Encargo{}, err
	}
	if tag.RowsAffected() == 0 {
		if _, err := s.Encargo(ctx, id); err != nil {
			return Encargo{}, err // ErrNotFound
		}
		return Encargo{}, ErrEncargoState
	}
	if err := logEncargo(ctx, tx, id, who, what, note); err != nil {
		return Encargo{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Encargo{}, err
	}
	return s.Encargo(ctx, id)
}

// TakeEncargo gives a pending encargo to an agent, in its cargo. Only one
// agent can have it: a second one taking it gets ErrEncargoState.
func (s *Store) TakeEncargo(ctx context.Context, id, keyID, cargoID int64, who string) (Encargo, error) {
	return s.change(ctx, id, who, "taken", "",
		`UPDATE encargos SET status = 'taken', key_id = $2, cargo_id = $3, updated_at = now() WHERE id = $1 AND status = 'pending'`,
		id, keyID, cargoID)
}

// WaitEncargo notes that the agent that has it waits on a run.
func (s *Store) WaitEncargo(ctx context.Context, id, keyID, runID int64, who string) (Encargo, error) {
	return s.change(ctx, id, who, "waiting", "",
		`UPDATE encargos SET status = 'waiting', waiting_run = $3, updated_at = now() WHERE id = $1 AND key_id = $2 AND status IN ('taken', 'waiting')`,
		id, keyID, runID)
}

// ResumeEncargo is the end of a wait: the run finished (note says how).
func (s *Store) ResumeEncargo(ctx context.Context, id, keyID int64, who, note string) (Encargo, error) {
	return s.change(ctx, id, who, "resumed", note,
		`UPDATE encargos SET status = 'taken', waiting_run = NULL, updated_at = now() WHERE id = $1 AND key_id = $2 AND status = 'waiting'`,
		id, keyID)
}

// DeliverEncargo hands an encargo over with its result and links.
func (s *Store) DeliverEncargo(ctx context.Context, id, keyID int64, who, result string, links []string) (Encargo, error) {
	if links == nil {
		links = []string{}
	}
	return s.change(ctx, id, who, "delivered", "",
		`UPDATE encargos SET status = 'delivered', waiting_run = NULL, result = $3, links = $4, updated_at = now()
		 WHERE id = $1 AND key_id = $2 AND status IN ('taken', 'waiting')`,
		id, keyID, result, links)
}

// ReviewEncargo is the person's answer to a delivered encargo: accepted
// (reviewed), or sent back with a comment, pending again for any agent.
func (s *Store) ReviewEncargo(ctx context.Context, id int64, who string, accept bool, comment string) (Encargo, error) {
	if accept {
		return s.change(ctx, id, who, "reviewed", comment,
			`UPDATE encargos SET status = 'reviewed', comment = $2, updated_at = now() WHERE id = $1 AND status = 'delivered'`, id, comment)
	}
	return s.change(ctx, id, who, "returned", comment,
		`UPDATE encargos SET status = 'pending', key_id = NULL, cargo_id = NULL, comment = $2, updated_at = now() WHERE id = $1 AND status = 'delivered'`,
		id, comment)
}

// ReleaseEncargo gives a taken encargo back (pending), e.g. when its agent
// cannot finish it; note says why.
func (s *Store) ReleaseEncargo(ctx context.Context, id, keyID int64, who, note string) (Encargo, error) {
	return s.change(ctx, id, who, "released", note,
		`UPDATE encargos SET status = 'pending', key_id = NULL, cargo_id = NULL, waiting_run = NULL, updated_at = now()
		 WHERE id = $1 AND key_id = $2 AND status IN ('taken', 'waiting')`,
		id, keyID)
}

// CancelEncargo cancels an encargo that is not finished.
func (s *Store) CancelEncargo(ctx context.Context, id int64, who, note string) (Encargo, error) {
	return s.change(ctx, id, who, "cancelled", note,
		`UPDATE encargos SET status = 'cancelled', waiting_run = NULL, updated_at = now() WHERE id = $1 AND status NOT IN ('reviewed', 'cancelled')`, id)
}
