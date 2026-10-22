-- Organizations: everything belongs to one (apps, the Ruta, encargos, agent
-- keys), and people belong to organizations with a role. Everything that
-- existed goes to organization 1; org_id defaults to it, so the previous
-- version keeps working during an update.
CREATE TABLE IF NOT EXISTS orgs (
    id         BIGSERIAL PRIMARY KEY,
    slug       TEXT NOT NULL UNIQUE,   -- in addresses: lowercase letters, digits, dashes
    name       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO orgs (id, slug, name) VALUES (1, 'principal', 'Principal') ON CONFLICT (id) DO NOTHING;
SELECT setval(pg_get_serial_sequence('orgs', 'id'), GREATEST((SELECT max(id) FROM orgs), 1));

-- People in an organization: guest (reads), collaborator (also runs CI,
-- writes the Ruta, asks agents), admin (also deploys, rolls back, secrets,
-- keys, add-ons, members).
CREATE TABLE IF NOT EXISTS org_members (
    org_id     BIGINT NOT NULL REFERENCES orgs(id),
    login      TEXT NOT NULL,          -- GitHub login, as GitHub writes it
    role       TEXT NOT NULL CHECK (role IN ('guest', 'collaborator', 'admin')),
    added_by   TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS org_members_login ON org_members (org_id, lower(login));

ALTER TABLE apps         ADD COLUMN IF NOT EXISTS org_id BIGINT NOT NULL DEFAULT 1 REFERENCES orgs(id);
ALTER TABLE agent_keys   ADD COLUMN IF NOT EXISTS org_id BIGINT NOT NULL DEFAULT 1 REFERENCES orgs(id);
ALTER TABLE ruta_entries ADD COLUMN IF NOT EXISTS org_id BIGINT NOT NULL DEFAULT 1 REFERENCES orgs(id);
ALTER TABLE encargos     ADD COLUMN IF NOT EXISTS org_id BIGINT NOT NULL DEFAULT 1 REFERENCES orgs(id);
