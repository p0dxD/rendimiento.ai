-- Post-deploy tasks: commands run against a release once it is live (smoke
-- tests, migrations), part of its verification.
CREATE TABLE release_tasks (
    release_id  BIGINT NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    status      TEXT NOT NULL,              -- running, succeeded, failed, skipped
    optional    BOOLEAN NOT NULL DEFAULT false,
    message     TEXT NOT NULL DEFAULT '',
    log         TEXT NOT NULL DEFAULT '',   -- the tail, at most 256 KiB
    started_at  TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    PRIMARY KEY (release_id, name)
);
