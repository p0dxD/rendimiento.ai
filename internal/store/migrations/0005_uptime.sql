-- Uptime checks. Every minute the platform checks each service of each app
-- (its health endpoint inside the cluster, and its public URL), and keeps:
--   probes        every check, for 7 days (the 24-hour charts)
--   probe_hourly  hourly summaries, for 400 days (7- and 30-day charts)
--   incidents     outages: two failed checks in a row until the next success
CREATE TABLE probes (
    app_id     BIGINT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    service    TEXT NOT NULL,
    kind       TEXT NOT NULL,              -- internal | public
    at         TIMESTAMPTZ NOT NULL,
    ok         BOOLEAN NOT NULL,
    status     INT NOT NULL DEFAULT 0,     -- HTTP status; 0 for TCP checks and errors
    latency_ms INT NOT NULL,
    error      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX probes_app_at ON probes (app_id, at);
CREATE INDEX probes_at ON probes (at);

CREATE TABLE probe_hourly (
    app_id  BIGINT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    service TEXT NOT NULL,
    kind    TEXT NOT NULL,
    hour    TIMESTAMPTZ NOT NULL,
    total   INT NOT NULL,
    ok      INT NOT NULL,
    p50_ms  INT NOT NULL,                  -- of successful checks
    p95_ms  INT NOT NULL,
    PRIMARY KEY (app_id, service, kind, hour)
);

CREATE TABLE incidents (
    id         BIGSERIAL PRIMARY KEY,
    app_id     BIGINT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    service    TEXT NOT NULL,
    kind       TEXT NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,       -- the first failed check
    ended_at   TIMESTAMPTZ,                -- the first successful check after; NULL while ongoing
    error      TEXT NOT NULL DEFAULT ''    -- what the first failure said
);
CREATE INDEX incidents_app ON incidents (app_id, started_at);
