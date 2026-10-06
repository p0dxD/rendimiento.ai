-- La Ruta: what the platform's people and agents know, kept in the platform
-- rather than in any one agent's session, so the next one (another model, or
-- a person) can pick up where the last left off.

-- Agent keys: bearer tokens for the MCP endpoint (/mcp). Created by a signed-in
-- person, shown once, stored as a SHA-256 hash, always expiring.
CREATE TABLE agent_keys (
    id           BIGSERIAL PRIMARY KEY,
    name         TEXT NOT NULL,                 -- e.g. "claude en main"
    token_hash   TEXT NOT NULL UNIQUE,
    can_write    BOOLEAN NOT NULL DEFAULT false, -- false: read only
    created_by   TEXT NOT NULL,                 -- GitHub login
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ NOT NULL,
    last_used_at TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ
);

-- Cargos: an agent's turn of work. Taken with a purpose, handed over with a
-- summary (entrega) and what is left (pendientes).
CREATE TABLE cargos (
    id          BIGSERIAL PRIMARY KEY,
    key_id      BIGINT NOT NULL REFERENCES agent_keys(id),
    purpose     TEXT NOT NULL,
    started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at    TIMESTAMPTZ,
    entrega     TEXT NOT NULL DEFAULT '',
    pendientes  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX cargos_started ON cargos (started_at DESC);
-- At most one open cargo per key.
CREATE UNIQUE INDEX cargos_one_open ON cargos (key_id) WHERE ended_at IS NULL;

-- Entries: decisions, manuals, pending work, notes, and lo vivido (what a
-- person lived and told, kept in their words; only people write those).
CREATE TABLE ruta_entries (
    id          BIGSERIAL PRIMARY KEY,
    kind        TEXT NOT NULL CHECK (kind IN ('decision', 'manual', 'pendiente', 'nota', 'vivido')),
    title       TEXT NOT NULL,
    body        TEXT NOT NULL DEFAULT '',
    tags        TEXT[] NOT NULL DEFAULT '{}',
    done        BOOLEAN NOT NULL DEFAULT false, -- for pendientes
    author      TEXT NOT NULL,                  -- GitHub login, or the agent key's name
    by_agent    BOOLEAN NOT NULL,
    cargo_id    BIGINT REFERENCES cargos(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    archived_at TIMESTAMPTZ
);
CREATE INDEX ruta_entries_updated ON ruta_entries (updated_at DESC);

-- Every earlier version of an entry, so nothing an edit replaces is lost.
CREATE TABLE ruta_history (
    id         BIGSERIAL PRIMARY KEY,
    entry_id   BIGINT NOT NULL REFERENCES ruta_entries(id),
    title      TEXT NOT NULL,
    body       TEXT NOT NULL,
    tags       TEXT[] NOT NULL,
    done       BOOLEAN NOT NULL,
    changed_by TEXT NOT NULL,
    changed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- What agents did through /mcp, one row per tool call.
CREATE TABLE agent_actions (
    id       BIGSERIAL PRIMARY KEY,
    key_id   BIGINT NOT NULL REFERENCES agent_keys(id),
    cargo_id BIGINT REFERENCES cargos(id),
    tool     TEXT NOT NULL,
    args     TEXT NOT NULL DEFAULT '', -- truncated
    ok       BOOLEAN NOT NULL,
    error    TEXT NOT NULL DEFAULT '',
    at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX agent_actions_at ON agent_actions (at DESC);
