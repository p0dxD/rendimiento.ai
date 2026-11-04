package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// Secreto is one sealed secret as kept: its nonce and ciphertext. The
// database never sees the value or the key (see internal/secretos).
type Secreto struct {
	Nonce   []byte
	Cifrado []byte
}

// LeerSecreto returns a sealed secret, or ErrNotFound.
func (s *Store) LeerSecreto(ctx context.Context, nombre string) (Secreto, error) {
	var sec Secreto
	err := s.pool.QueryRow(ctx, `SELECT nonce, cifrado FROM secretos WHERE nombre = $1`, nombre).Scan(&sec.Nonce, &sec.Cifrado)
	if errors.Is(err, pgx.ErrNoRows) {
		return Secreto{}, ErrNotFound
	}
	return sec, err
}

// GuardarSecreto keeps a sealed secret, replacing the one by that name.
func (s *Store) GuardarSecreto(ctx context.Context, nombre string, sec Secreto) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO secretos (nombre, nonce, cifrado) VALUES ($1, $2, $3)
		ON CONFLICT (nombre) DO UPDATE SET nonce = EXCLUDED.nonce, cifrado = EXCLUDED.cifrado, actualizado = now()`,
		nombre, sec.Nonce, sec.Cifrado)
	return err
}

// BorrarSecreto removes a sealed secret; one that is not there is gone already.
func (s *Store) BorrarSecreto(ctx context.Context, nombre string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM secretos WHERE nombre = $1`, nombre)
	return err
}
