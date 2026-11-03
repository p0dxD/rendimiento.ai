-- Demo links: a secret address that opens a read-only session in an
-- organization, for people without a GitHub account here (reviewers,
-- investors). Each link signs in as its own guest, demo~<id>, which a GitHub
-- login can never be; only the code's hash is kept.
CREATE TABLE IF NOT EXISTS demo_links (
    id           BIGSERIAL PRIMARY KEY,
    org_id       BIGINT NOT NULL REFERENCES orgs(id),
    code_hash    TEXT NOT NULL UNIQUE,
    note         TEXT NOT NULL DEFAULT '',
    created_by   TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ NOT NULL,
    uses         INTEGER NOT NULL DEFAULT 0,
    last_used_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS demo_links_org ON demo_links (org_id);
