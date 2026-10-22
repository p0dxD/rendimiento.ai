package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// People's roles in an organization, from least to most trusted.
const (
	MemberGuest        = "guest"        // reads everything in the organization
	MemberCollaborator = "collaborator" // also runs CI, writes the Ruta, asks agents
	MemberAdmin        = "admin"        // also deploys, rolls back, secrets, keys, add-ons, members
)

// DefaultOrg is the organization everything belonged to before there were
// several.
const DefaultOrg int64 = 1

// RoleRank orders roles: a route asks for a minimum rank (0 for unknown).
func RoleRank(role string) int {
	switch role {
	case MemberGuest:
		return 1
	case MemberCollaborator:
		return 2
	case MemberAdmin:
		return 3
	}
	return 0
}

// ErrLastAdmin refuses leaving an organization without an administrator.
var ErrLastAdmin = errors.New("an organization needs at least one administrator")

// Member is a person in an organization.
type Member struct {
	Login     string    `json:"login"`
	Role      string    `json:"role"`
	AddedBy   string    `json:"addedBy"`
	CreatedAt time.Time `json:"createdAt"`
}

// MemberRole is a person's role in an organization (ErrNotFound when they
// are not in it).
func (s *Store) MemberRole(ctx context.Context, orgID int64, login string) (string, error) {
	var role string
	err := s.pool.QueryRow(ctx, `SELECT role FROM org_members WHERE org_id = $1 AND lower(login) = lower($2)`, orgID, login).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return role, err
}

// Members lists an organization's people, administrators first.
func (s *Store) Members(ctx context.Context, orgID int64) ([]Member, error) {
	rows, err := s.pool.Query(ctx, `SELECT login, role, added_by, created_at FROM org_members WHERE org_id = $1
		ORDER BY CASE role WHEN 'admin' THEN 0 WHEN 'collaborator' THEN 1 ELSE 2 END, lower(login)`, orgID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Member, error) {
		var m Member
		err := r.Scan(&m.Login, &m.Role, &m.AddedBy, &m.CreatedAt)
		return m, err
	})
}

// SetMember adds a person to an organization, or changes their role; an
// organization never loses its last administrator.
func (s *Store) SetMember(ctx context.Context, orgID int64, login, role, by string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if role != MemberAdmin {
		if err := lastAdmin(ctx, tx, orgID, login); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO org_members (org_id, login, role, added_by) VALUES ($1, $2, $3, $4)
		ON CONFLICT (org_id, lower(login)) DO UPDATE SET role = EXCLUDED.role`, orgID, strings.TrimSpace(login), role, by); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RemoveMember takes a person out of an organization (not its last
// administrator).
func (s *Store) RemoveMember(ctx context.Context, orgID int64, login string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lastAdmin(ctx, tx, orgID, login); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM org_members WHERE org_id = $1 AND lower(login) = lower($2)`, orgID, login)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

// lastAdmin refuses a change that would take away the organization's only
// administrator (login is the person losing the role). The rows are locked
// so two changes at once cannot both pass.
func lastAdmin(ctx context.Context, tx pgx.Tx, orgID int64, login string) error {
	rows, err := tx.Query(ctx, `SELECT login FROM org_members WHERE org_id = $1 AND role = 'admin' FOR UPDATE`, orgID)
	if err != nil {
		return err
	}
	admins, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	if len(admins) == 1 && strings.EqualFold(admins[0], login) {
		return ErrLastAdmin
	}
	return nil
}

// IsMemberAnywhere reports whether a person belongs to any organization:
// who may sign in.
func (s *Store) IsMemberAnywhere(ctx context.Context, login string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM org_members WHERE lower(login) = lower($1))`, login).Scan(&ok)
	return ok, err
}

// SeedAdmins makes the logins administrators of the default organization
// when it has no members yet: ALLOWED_USERS, the way people were let in
// before organizations, starts it off. Afterwards members are managed in
// the UI, and the setting no longer counts.
func (s *Store) SeedAdmins(ctx context.Context, logins []string) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `LOCK TABLE org_members IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return 0, err
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM org_members WHERE org_id = $1`, DefaultOrg).Scan(&n); err != nil || n > 0 {
		return 0, err
	}
	added := 0
	for _, l := range logins {
		if l = strings.TrimSpace(l); l == "" {
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO org_members (org_id, login, role, added_by) VALUES ($1, $2, 'admin', 'ALLOWED_USERS')
			ON CONFLICT (org_id, lower(login)) DO NOTHING`, DefaultOrg, l); err != nil {
			return 0, err
		}
		added++
	}
	return added, tx.Commit(ctx)
}

// Org is an organization, with the role of the person asking when listed
// for them.
type Org struct {
	ID        int64     `json:"id"`
	Slug      string    `json:"slug"`
	Name      string    `json:"name"`
	Role      string    `json:"role,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// ErrSlugTaken is a new organization's address already in use.
var ErrSlugTaken = errors.New("that address is taken by another organization")

// OrgsOf lists the organizations a person belongs to, with their role, the
// default one first.
func (s *Store) OrgsOf(ctx context.Context, login string) ([]Org, error) {
	rows, err := s.pool.Query(ctx, `SELECT o.id, o.slug, o.name, m.role, o.created_at FROM orgs o
		JOIN org_members m ON m.org_id = o.id WHERE lower(m.login) = lower($1) ORDER BY o.id`, login)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Org, error) {
		var o Org
		err := r.Scan(&o.ID, &o.Slug, &o.Name, &o.Role, &o.CreatedAt)
		return o, err
	})
}

// GetOrg returns one organization.
func (s *Store) GetOrg(ctx context.Context, id int64) (Org, error) {
	var o Org
	err := s.pool.QueryRow(ctx, `SELECT id, slug, name, created_at FROM orgs WHERE id = $1`, id).Scan(&o.ID, &o.Slug, &o.Name, &o.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, ErrNotFound
	}
	return o, err
}

// CreateOrg makes an organization with its first administrator.
func (s *Store) CreateOrg(ctx context.Context, slug, name, admin string) (Org, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Org{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var o Org
	err = tx.QueryRow(ctx, `INSERT INTO orgs (slug, name) VALUES ($1, $2) ON CONFLICT (slug) DO NOTHING RETURNING id, slug, name, created_at`,
		slug, name).Scan(&o.ID, &o.Slug, &o.Name, &o.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, ErrSlugTaken
	}
	if err != nil {
		return o, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO org_members (org_id, login, role, added_by) VALUES ($1, $2, 'admin', $2)`, o.ID, admin); err != nil {
		return o, err
	}
	o.Role = MemberAdmin
	return o, tx.Commit(ctx)
}
