package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Puesto is a page of the Mercado: an app made from a form (see
// internal/mercado). Its content lives in the app's repository.
type Puesto struct {
	AppID     int64     `json:"appId"`
	App       string    `json:"app"`
	Owner     string    `json:"owner"`
	Tipo      string    `json:"tipo"`
	Nombre    string    `json:"nombre"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

const puestoCols = `p.app_id, a.name, p.owner, p.tipo, p.nombre, p.created_at, p.updated_at`

func scanPuesto(row interface{ Scan(...any) error }) (*Puesto, error) {
	var p Puesto
	if err := row.Scan(&p.AppID, &p.App, &p.Owner, &p.Tipo, &p.Nombre, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	return &p, nil
}

// CreatePuesto records that app is a page of the Mercado.
func (s *Store) CreatePuesto(ctx context.Context, p *Puesto) error {
	return s.pool.QueryRow(ctx,
		`INSERT INTO puestos (app_id, owner, tipo, nombre) VALUES ($1, $2, $3, $4) RETURNING created_at, updated_at`,
		p.AppID, p.Owner, p.Tipo, p.Nombre).Scan(&p.CreatedAt, &p.UpdatedAt)
}

// UpdatePuesto keeps the Mercado's card in step with a saved page.
func (s *Store) UpdatePuesto(ctx context.Context, appID int64, tipo, nombre string) error {
	_, err := s.pool.Exec(ctx, `UPDATE puestos SET tipo = $2, nombre = $3, updated_at = now() WHERE app_id = $1`, appID, tipo, nombre)
	return err
}

// GetPuesto returns the page of an app, or ErrNotFound when the app is not one.
func (s *Store) GetPuesto(ctx context.Context, appID int64) (*Puesto, error) {
	p, err := scanPuesto(s.pool.QueryRow(ctx, `SELECT `+puestoCols+` FROM puestos p JOIN apps a ON a.id = p.app_id WHERE p.app_id = $1`, appID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// ListPuestos lists the Mercado, most recently changed first.
func (s *Store) ListPuestos(ctx context.Context) ([]*Puesto, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+puestoCols+` FROM puestos p JOIN apps a ON a.id = p.app_id ORDER BY p.updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Puesto{}
	for rows.Next() {
		p, err := scanPuesto(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
