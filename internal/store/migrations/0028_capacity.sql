-- The platform's own settings, changed from the UI without a redeploy: the
-- capacity (how many runs at once, how many per app, the most CPU a step
-- asks for, the nodes that take no runs). Each value overrides its
-- environment variable; a missing one keeps it.
CREATE TABLE IF NOT EXISTS platform_settings (
    key        TEXT PRIMARY KEY,
    value      JSONB NOT NULL,
    updated_by TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
