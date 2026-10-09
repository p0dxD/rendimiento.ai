package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Ruta entry kinds. People write every kind; agents write all but Vivido.
const (
	KindDecision  = "decision"
	KindManual    = "manual"
	KindPendiente = "pendiente"
	KindNota      = "nota"
	KindVivido    = "vivido"
)

// ErrCargoOpen is returned when a key already has an open cargo.
var ErrCargoOpen = errors.New("this key already has an open cargo")

// AgentKey is a bearer token for the MCP endpoint (the token itself is never stored).
type AgentKey struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	CanWrite   bool       `json:"canWrite"`
	CreatedBy  string     `json:"createdBy"`
	CreatedAt  time.Time  `json:"createdAt"`
	ExpiresAt  time.Time  `json:"expiresAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
}

// Usable reports whether the key may be used now.
func (k AgentKey) Usable(now time.Time) bool {
	return k.RevokedAt == nil && now.Before(k.ExpiresAt)
}

// Cargo is an agent's turn of work.
type Cargo struct {
	ID         int64      `json:"id"`
	KeyID      int64      `json:"keyId"`
	KeyName    string     `json:"keyName"`
	Purpose    string     `json:"purpose"`
	StartedAt  time.Time  `json:"startedAt"`
	EndedAt    *time.Time `json:"endedAt,omitempty"`
	Entrega    string     `json:"entrega"`
	Pendientes string     `json:"pendientes"`
}

// RutaEntry is one thing the platform knows: a decision, a manual, a pending
// task, a note, or something a person lived.
type RutaEntry struct {
	ID         int64      `json:"id"`
	Kind       string     `json:"kind"`
	Title      string     `json:"title"`
	Body       string     `json:"body"`
	Tags       []string   `json:"tags"`
	Done       bool       `json:"done"`
	Author     string     `json:"author"`
	ByAgent    bool       `json:"byAgent"`
	CargoID    *int64     `json:"cargoId,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
	ArchivedAt *time.Time `json:"archivedAt,omitempty"`
}

// AgentAction is one tool call made through the MCP endpoint.
type AgentAction struct {
	KeyID   int64
	CargoID *int64
	Tool    string
	Args    string
	OK      bool
	Error   string
}

// ---- agent keys ----

// CreateAgentKey stores a new key by its token's hash.
func (s *Store) CreateAgentKey(ctx context.Context, name, tokenHash string, canWrite bool, createdBy string, expires time.Time) (AgentKey, error) {
	k := AgentKey{Name: name, CanWrite: canWrite, CreatedBy: createdBy, ExpiresAt: expires}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO agent_keys (name, token_hash, can_write, created_by, expires_at)
		VALUES ($1, $2, $3, $4, $5) RETURNING id, created_at`,
		name, tokenHash, canWrite, createdBy, expires).Scan(&k.ID, &k.CreatedAt)
	return k, err
}

const agentKeyCols = `id, name, can_write, created_by, created_at, expires_at, last_used_at, revoked_at`

// scanAgentKey reads an agent key row (never the key: only its hash is
// stored).
func scanAgentKey(row pgx.Row) (AgentKey, error) {
	var k AgentKey
	err := row.Scan(&k.ID, &k.Name, &k.CanWrite, &k.CreatedBy, &k.CreatedAt, &k.ExpiresAt, &k.LastUsedAt, &k.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return k, ErrNotFound
	}
	return k, err
}

// AgentKeyByHash finds a key by its token's hash, and notes that it was used
// (at most once a minute, to keep writes down).
func (s *Store) AgentKeyByHash(ctx context.Context, tokenHash string) (AgentKey, error) {
	k, err := scanAgentKey(s.pool.QueryRow(ctx, `SELECT `+agentKeyCols+` FROM agent_keys WHERE token_hash = $1`, tokenHash))
	if err != nil {
		return k, err
	}
	if k.LastUsedAt == nil || time.Since(*k.LastUsedAt) > time.Minute {
		_, _ = s.pool.Exec(ctx, `UPDATE agent_keys SET last_used_at = now() WHERE id = $1`, k.ID)
	}
	return k, nil
}

// AgentKey returns one key.
func (s *Store) AgentKey(ctx context.Context, id int64) (AgentKey, error) {
	return scanAgentKey(s.pool.QueryRow(ctx, `SELECT `+agentKeyCols+` FROM agent_keys WHERE id = $1`, id))
}

// ListAgentKeys returns every key, newest first.
func (s *Store) ListAgentKeys(ctx context.Context) ([]AgentKey, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+agentKeyCols+` FROM agent_keys ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AgentKey{}
	for rows.Next() {
		k, err := scanAgentKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// RevokeAgentKey stops a key from working, and closes its open cargo.
func (s *Store) RevokeAgentKey(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `UPDATE agent_keys SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	_, err = s.pool.Exec(ctx, `UPDATE cargos SET ended_at = now(), entrega = 'revoked: the key was revoked before handing over' WHERE key_id = $1 AND ended_at IS NULL`, id)
	return err
}

// ---- cargos ----

const cargoCols = `c.id, c.key_id, k.name, c.purpose, c.started_at, c.ended_at, c.entrega, c.pendientes`

// scanCargo reads a cargo row.
func scanCargo(row pgx.Row) (Cargo, error) {
	var c Cargo
	err := row.Scan(&c.ID, &c.KeyID, &c.KeyName, &c.Purpose, &c.StartedAt, &c.EndedAt, &c.Entrega, &c.Pendientes)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

// TakeCargo opens a cargo for a key; ErrCargoOpen if it already has one.
func (s *Store) TakeCargo(ctx context.Context, keyID int64, purpose string) (Cargo, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO cargos (key_id, purpose) VALUES ($1, $2) RETURNING id`, keyID, purpose).Scan(&id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return Cargo{}, ErrCargoOpen
	}
	if err != nil {
		return Cargo{}, err
	}
	return s.Cargo(ctx, id)
}

// Cargo returns one cargo.
func (s *Store) Cargo(ctx context.Context, id int64) (Cargo, error) {
	return scanCargo(s.pool.QueryRow(ctx, `SELECT `+cargoCols+` FROM cargos c JOIN agent_keys k ON k.id = c.key_id WHERE c.id = $1`, id))
}

// OpenCargo returns a key's open cargo, or ErrNotFound.
func (s *Store) OpenCargo(ctx context.Context, keyID int64) (Cargo, error) {
	return scanCargo(s.pool.QueryRow(ctx, `SELECT `+cargoCols+` FROM cargos c JOIN agent_keys k ON k.id = c.key_id WHERE c.key_id = $1 AND c.ended_at IS NULL`, keyID))
}

// HandOver closes a cargo with its entrega and what is left.
func (s *Store) HandOver(ctx context.Context, id int64, entrega, pendientes string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE cargos SET ended_at = now(), entrega = $2, pendientes = $3 WHERE id = $1 AND ended_at IS NULL`, id, entrega, pendientes)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListCargos returns cargos, newest first.
func (s *Store) ListCargos(ctx context.Context, limit int) ([]Cargo, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+cargoCols+` FROM cargos c JOIN agent_keys k ON k.id = c.key_id ORDER BY c.started_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Cargo{}
	for rows.Next() {
		c, err := scanCargo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---- entries ----

const rutaCols = `id, kind, title, body, tags, done, author, by_agent, cargo_id, created_at, updated_at, archived_at`

// scanRuta reads a Ruta entry row.
func scanRuta(row pgx.Row) (RutaEntry, error) {
	var e RutaEntry
	err := row.Scan(&e.ID, &e.Kind, &e.Title, &e.Body, &e.Tags, &e.Done, &e.Author, &e.ByAgent, &e.CargoID, &e.CreatedAt, &e.UpdatedAt, &e.ArchivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return e, ErrNotFound
	}
	if e.Tags == nil {
		e.Tags = []string{}
	}
	return e, err
}

// AddRuta stores a new entry.
func (s *Store) AddRuta(ctx context.Context, e RutaEntry) (RutaEntry, error) {
	if e.Tags == nil {
		e.Tags = []string{}
	}
	return scanRuta(s.pool.QueryRow(ctx, `
		INSERT INTO ruta_entries (kind, title, body, tags, done, author, by_agent, cargo_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING `+rutaCols,
		e.Kind, e.Title, e.Body, e.Tags, e.Done, e.Author, e.ByAgent, e.CargoID))
}

// Ruta returns one entry.
func (s *Store) Ruta(ctx context.Context, id int64) (RutaEntry, error) {
	return scanRuta(s.pool.QueryRow(ctx, `SELECT `+rutaCols+` FROM ruta_entries WHERE id = $1`, id))
}

// UpdateRuta replaces an entry's title, body, tags and done, keeping the
// previous version in ruta_history.
func (s *Store) UpdateRuta(ctx context.Context, e RutaEntry, changedBy string) (RutaEntry, error) {
	if e.Tags == nil {
		e.Tags = []string{}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return e, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
		INSERT INTO ruta_history (entry_id, title, body, tags, done, changed_by)
		SELECT id, title, body, tags, done, $2 FROM ruta_entries WHERE id = $1`, e.ID, changedBy)
	if err != nil {
		return e, err
	}
	if tag.RowsAffected() == 0 {
		return e, ErrNotFound
	}
	out, err := scanRuta(tx.QueryRow(ctx, `
		UPDATE ruta_entries SET title = $2, body = $3, tags = $4, done = $5, updated_at = now()
		WHERE id = $1 RETURNING `+rutaCols, e.ID, e.Title, e.Body, e.Tags, e.Done))
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

// ArchiveRuta hides an entry from lists (it is never deleted).
func (s *Store) ArchiveRuta(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `UPDATE ruta_entries SET archived_at = now() WHERE id = $1 AND archived_at IS NULL`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SearchRuta lists entries that are not archived, newest change first. kind
// filters ("" for all); text matches the title, body or a tag, ignoring case.
func (s *Store) SearchRuta(ctx context.Context, kind, text string, limit int) ([]RutaEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+rutaCols+` FROM ruta_entries
		WHERE archived_at IS NULL AND ($1 = '' OR kind = $1)
		  AND ($2 = '' OR title ILIKE '%' || $2 || '%' OR body ILIKE '%' || $2 || '%' OR $2 ILIKE ANY (tags))
		ORDER BY updated_at DESC LIMIT $3`, kind, text, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RutaEntry{}
	for rows.Next() {
		e, err := scanRuta(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---- audit ----

// RecordAgentAction notes one tool call.
func (s *Store) RecordAgentAction(ctx context.Context, a AgentAction) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO agent_actions (key_id, cargo_id, tool, args, ok, error) VALUES ($1, $2, $3, $4, $5, $6)`,
		a.KeyID, a.CargoID, a.Tool, a.Args, a.OK, a.Error)
	return err
}

// AgentActionRow is a recorded tool call, for the UI.
type AgentActionRow struct {
	ID      int64     `json:"id"`
	KeyName string    `json:"keyName"`
	CargoID *int64    `json:"cargoId,omitempty"`
	Tool    string    `json:"tool"`
	Args    string    `json:"args"`
	OK      bool      `json:"ok"`
	Error   string    `json:"error,omitempty"`
	At      time.Time `json:"at"`
}

// ListAgentActions returns the latest tool calls, newest first.
func (s *Store) ListAgentActions(ctx context.Context, limit int) ([]AgentActionRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, k.name, a.cargo_id, a.tool, a.args, a.ok, a.error, a.at
		FROM agent_actions a JOIN agent_keys k ON k.id = a.key_id ORDER BY a.at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AgentActionRow{}
	for rows.Next() {
		var a AgentActionRow
		if err := rows.Scan(&a.ID, &a.KeyName, &a.CargoID, &a.Tool, &a.Args, &a.OK, &a.Error, &a.At); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
