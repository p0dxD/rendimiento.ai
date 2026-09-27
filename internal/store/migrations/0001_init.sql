CREATE TABLE apps (
    id              BIGSERIAL PRIMARY KEY,
    name            TEXT NOT NULL UNIQUE,
    repo            TEXT NOT NULL,              -- owner/name
    installation_id BIGINT NOT NULL,
    default_branch  TEXT NOT NULL DEFAULT 'main',
    spec            JSONB NOT NULL,             -- last accepted rendimiento.yaml
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX apps_repo ON apps (repo);

CREATE TABLE runs (
    id          BIGSERIAL PRIMARY KEY,
    app_id      BIGINT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    sha         TEXT NOT NULL,
    branch      TEXT NOT NULL,
    event       TEXT NOT NULL,                 -- push | pull_request | manual
    deploy      BOOLEAN NOT NULL,
    status      TEXT NOT NULL DEFAULT 'queued', -- queued | running | succeeded | failed | cancelled
    message     TEXT NOT NULL DEFAULT '',
    check_run   BIGINT,                        -- GitHub check run id
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at  TIMESTAMPTZ,
    finished_at TIMESTAMPTZ
);
CREATE INDEX runs_app ON runs (app_id, id DESC);
CREATE INDEX runs_queued ON runs (id) WHERE status = 'queued';

CREATE TABLE steps (
    run_id      BIGINT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    step_id     TEXT NOT NULL,
    service     TEXT NOT NULL,
    kind        TEXT NOT NULL,
    depends_on  TEXT[] NOT NULL DEFAULT '{}',
    status      TEXT NOT NULL DEFAULT 'pending',
    digest      TEXT NOT NULL DEFAULT '',
    message     TEXT NOT NULL DEFAULT '',
    started_at  TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    log         TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (run_id, step_id)
);

CREATE TABLE releases (
    id         BIGSERIAL PRIMARY KEY,
    app_id     BIGINT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    number     BIGINT NOT NULL,
    run_id     BIGINT REFERENCES runs(id) ON DELETE SET NULL,
    sha        TEXT NOT NULL,
    images     JSONB NOT NULL,                 -- service → image@digest
    spec       JSONB NOT NULL,
    rollback_of BIGINT,                         -- release number this one restores
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (app_id, number)
);

CREATE TABLE sessions (
    token_hash TEXT PRIMARY KEY,
    login      TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);
