package secretos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	gh "github.com/p0dxD/rendimiento.ai/internal/github"
)

// Credenciales keeps the GitHub App's credentials in the Caja (as
// api.CredentialStore).
type Credenciales struct{ Caja *Caja }

const nombreGitHub = "github-app"

// ErrYaHay: the GitHub App's credentials are kept already; they are never
// silently replaced.
var ErrYaHay = errors.New("the GitHub App's credentials are already kept")

// Load returns the credentials, or nil before setup has run.
func (c Credenciales) Load(ctx context.Context) (*gh.Credentials, error) {
	raw, ok, err := c.Caja.Leer(ctx, nombreGitHub)
	if err != nil || !ok {
		return nil, err
	}
	var cr gh.Credentials
	if err := json.Unmarshal(raw, &cr); err != nil {
		return nil, err
	}
	return &cr, nil
}

// Save keeps the credentials the first time.
func (c Credenciales) Save(ctx context.Context, cr *gh.Credentials) error {
	if _, ok, err := c.Caja.Leer(ctx, nombreGitHub); err != nil {
		return err
	} else if ok {
		return ErrYaHay
	}
	raw, err := json.Marshal(cr)
	if err != nil {
		return err
	}
	return c.Caja.Guardar(ctx, nombreGitHub, raw)
}

// LlavesIA keeps each organization's own AI key in the Caja (as
// ia.SecretKeys does in Kubernetes); the UI only sees its last four
// characters.
type LlavesIA struct{ Caja *Caja }

func nombreIA(orgID int64) string { return fmt.Sprintf("ia-org-%d", orgID) }

// OrgKey is the organization's key ("" when it has none).
func (l LlavesIA) OrgKey(ctx context.Context, orgID int64) (string, error) {
	raw, _, err := l.Caja.Leer(ctx, nombreIA(orgID))
	return string(raw), err
}

// KeyHint is the key's last four characters ("" when none).
func (l LlavesIA) KeyHint(ctx context.Context, orgID int64) (string, error) {
	key, err := l.OrgKey(ctx, orgID)
	if err != nil || len(key) < 8 {
		return "", err
	}
	return "…" + key[len(key)-4:], nil
}

// SetOrgKey keeps the organization's key; an empty one removes it.
func (l LlavesIA) SetOrgKey(ctx context.Context, orgID int64, key string) error {
	if key == "" {
		return l.Caja.Borrar(ctx, nombreIA(orgID))
	}
	return l.Caja.Guardar(ctx, nombreIA(orgID), []byte(key))
}
