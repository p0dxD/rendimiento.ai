// Package secretos keeps the platform's own secrets (the GitHub App's
// credentials, organizations' AI keys) where there is no Kubernetes to keep
// them: sealed with AES-256-GCM in Postgres, by a key that lives outside
// the database, in a file only rendimiento reads. Each secret is bound to
// its name, so a sealed value copied under another name does not open.
package secretos

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// Almacen is where sealed secrets are kept (*store.Store).
type Almacen interface {
	LeerSecreto(ctx context.Context, nombre string) (store.Secreto, error)
	GuardarSecreto(ctx context.Context, nombre string, sec store.Secreto) error
	BorrarSecreto(ctx context.Context, nombre string) error
}

// Caja seals and opens secrets kept in an Almacen.
type Caja struct {
	almacen Almacen
	aead    cipher.AEAD
}

// LlaveLen is the key's length: AES-256.
const LlaveLen = 32

// Nueva makes a Caja with a 32-byte key.
func Nueva(a Almacen, llave []byte) (*Caja, error) {
	if len(llave) != LlaveLen {
		return nil, fmt.Errorf("the secrets key must be %d bytes, got %d", LlaveLen, len(llave))
	}
	block, err := aes.NewCipher(llave)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Caja{almacen: a, aead: aead}, nil
}

// LeerLlave reads a key file: 32 random bytes in base64, as NuevaLlave
// writes them.
func LeerLlave(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	llave, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil, fmt.Errorf("the secrets key in %s is not base64: %w", path, err)
	}
	return llave, nil
}

// NuevaLlave is a new random key, in base64, for a key file.
func NuevaLlave() string {
	b := make([]byte, LlaveLen)
	_, _ = rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

// ErrNoAbre: the sealed value does not open with this key (another key, or
// it was changed or moved).
var ErrNoAbre = errors.New("a kept secret does not open with this key")

// Leer opens the secret; ok is false when there is none by that name.
func (c *Caja) Leer(ctx context.Context, nombre string) (valor []byte, ok bool, err error) {
	sec, err := c.almacen.LeerSecreto(ctx, nombre)
	if errors.Is(err, store.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	valor, err = c.aead.Open(nil, sec.Nonce, sec.Cifrado, []byte(nombre))
	if err != nil {
		return nil, false, ErrNoAbre
	}
	return valor, true, nil
}

// Guardar seals the value under the name, with a new random nonce.
func (c *Caja) Guardar(ctx context.Context, nombre string, valor []byte) error {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	return c.almacen.GuardarSecreto(ctx, nombre, store.Secreto{Nonce: nonce, Cifrado: c.aead.Seal(nil, nonce, valor, []byte(nombre))})
}

// Borrar removes the secret.
func (c *Caja) Borrar(ctx context.Context, nombre string) error {
	return c.almacen.BorrarSecreto(ctx, nombre)
}
