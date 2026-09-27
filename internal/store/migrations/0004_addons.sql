-- Add-ons are optional platform features (Renovate first). Their settings
-- live here until add-on settings move to a git repo.
CREATE TABLE addons (
    name           TEXT PRIMARY KEY,
    enabled        BOOLEAN NOT NULL DEFAULT false,
    settings       JSONB NOT NULL DEFAULT '{}',
    last_scheduled TIMESTAMPTZ,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Per-app switches, e.g. Renovate on for this app's repo.
CREATE TABLE app_addons (
    app_id  BIGINT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    addon   TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT false,
    PRIMARY KEY (app_id, addon)
);

CREATE TABLE addon_runs (
    id          BIGSERIAL PRIMARY KEY,
    addon       TEXT NOT NULL,
    trigger     TEXT NOT NULL,
    status      TEXT NOT NULL,
    message     TEXT NOT NULL DEFAULT '',
    repos       JSONB NOT NULL DEFAULT '[]',
    results     JSONB NOT NULL DEFAULT '{}',
    log         TEXT NOT NULL DEFAULT '',
    started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ
);
CREATE INDEX addon_runs_recent ON addon_runs (addon, id DESC);
