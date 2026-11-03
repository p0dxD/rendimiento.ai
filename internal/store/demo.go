package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// demoPrefix starts a demo link's login: "~" is never in a GitHub login, so
// nobody can sign in with GitHub as one.
const demoPrefix = "demo~"

// DemoLogin is the guest a demo link signs in as.
func DemoLogin(id int64) string { return fmt.Sprintf("%s%d", demoPrefix, id) }

// IsDemoLogin reports a demo link's guest.
func IsDemoLogin(login string) bool { return strings.HasPrefix(strings.ToLower(login), demoPrefix) }

// DemoLink is a secret address that opens a read-only session in an
// organization (only the code's hash is kept).
type DemoLink struct {
	ID         int64      `json:"id"`
	OrgID      int64      `json:"orgId"`
	Note       string     `json:"note"`
	CreatedBy  string     `json:"createdBy"`
	CreatedAt  time.Time  `json:"createdAt"`
	ExpiresAt  time.Time  `json:"expiresAt"`
	Uses       int        `json:"uses"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
}

const demoCols = `id, org_id, note, created_by, created_at, expires_at, uses, last_used_at`

func scanDemo(r pgx.Row) (DemoLink, error) {
	var d DemoLink
	err := r.Scan(&d.ID, &d.OrgID, &d.Note, &d.CreatedBy, &d.CreatedAt, &d.ExpiresAt, &d.Uses, &d.LastUsedAt)
	return d, err
}

// ErrDemoPlatformOrg refuses a demo link to the platform's organization:
// its guests see the cluster's pages.
var ErrDemoPlatformOrg = errors.New("demo links are for an organization other than the platform's")

// CreateDemoLink keeps a new demo link and adds its guest to the
// organization.
func (s *Store) CreateDemoLink(ctx context.Context, orgID int64, codeHash, note, by string, expires time.Time) (*DemoLink, error) {
	if orgID == DefaultOrg {
		return nil, ErrDemoPlatformOrg
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	d, err := scanDemo(tx.QueryRow(ctx, `INSERT INTO demo_links (org_id, code_hash, note, created_by, expires_at)
		VALUES ($1, $2, $3, $4, $5) RETURNING `+demoCols, orgID, codeHash, note, by, expires))
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO org_members (org_id, login, role, added_by) VALUES ($1, $2, $3, $4)`,
		orgID, DemoLogin(d.ID), MemberGuest, by); err != nil {
		return nil, err
	}
	return &d, tx.Commit(ctx)
}

// DemoLinks lists an organization's demo links, newest first, after
// removing those that expired.
func (s *Store) DemoLinks(ctx context.Context, orgID int64) ([]DemoLink, error) {
	if err := s.purgeDemoLinks(ctx); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT `+demoCols+` FROM demo_links WHERE org_id = $1 ORDER BY id DESC`, orgID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (DemoLink, error) { return scanDemo(r) })
}

// OpenDemoLink finds the live demo link with this code's hash and counts
// the visit, or ErrNotFound (unknown, expired, or its guest was removed).
func (s *Store) OpenDemoLink(ctx context.Context, codeHash string) (*DemoLink, error) {
	d, err := scanDemo(s.pool.QueryRow(ctx, `UPDATE demo_links d SET uses = uses + 1, last_used_at = now()
		WHERE code_hash = $1 AND expires_at > now() AND org_id <> $2
		  AND EXISTS (SELECT 1 FROM org_members m WHERE m.org_id = d.org_id AND lower(m.login) = 'demo~' || d.id)
		RETURNING `+demoCols, codeHash, DefaultOrg))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// DeleteDemoLink turns a demo link off: the link, its guest and the guest's
// sessions go at once.
func (s *Store) DeleteDemoLink(ctx context.Context, orgID, id int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `DELETE FROM demo_links WHERE org_id = $1 AND id = $2`, orgID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := dropDemoGuests(ctx, tx, []string{DemoLogin(id)}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// purgeDemoLinks removes the demo links that expired, with their guests.
func (s *Store) purgeDemoLinks(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `DELETE FROM demo_links WHERE expires_at <= now() RETURNING 'demo~' || id`)
	if err != nil {
		return err
	}
	logins, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	if err := dropDemoGuests(ctx, tx, logins); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func dropDemoGuests(ctx context.Context, tx pgx.Tx, logins []string) error {
	if len(logins) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `DELETE FROM org_members WHERE lower(login) = ANY($1)`, logins); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM sessions WHERE lower(login) = ANY($1)`, logins)
	return err
}
