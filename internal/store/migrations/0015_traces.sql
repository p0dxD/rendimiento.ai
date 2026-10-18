-- Traces kept for the Universe's request waterfall, written by the traces
-- service (ROLE=traces). Both tables are partitioned by day (UTC), so old
-- days are dropped whole; the service creates and drops the partitions.
CREATE TABLE IF NOT EXISTS traces (
    trace_id    TEXT NOT NULL,
    started_at  TIMESTAMPTZ NOT NULL,
    duration_us BIGINT NOT NULL,
    service     TEXT NOT NULL,   -- the root span's service (app/service)
    name        TEXT NOT NULL,   -- the root span's name
    services    TEXT[] NOT NULL, -- every service the request went through
    spans       INT NOT NULL,
    error       BOOLEAN NOT NULL,
    reason      TEXT NOT NULL    -- why it was kept: error, slow or sample
) PARTITION BY RANGE (started_at);
CREATE INDEX IF NOT EXISTS traces_started ON traces (started_at DESC);
CREATE INDEX IF NOT EXISTS traces_services ON traces USING GIN (services);
CREATE INDEX IF NOT EXISTS traces_id ON traces (trace_id);

CREATE TABLE IF NOT EXISTS spans (
    trace_id       TEXT NOT NULL,
    span_id        TEXT NOT NULL,
    parent_id      TEXT NOT NULL,
    service        TEXT NOT NULL,
    name           TEXT NOT NULL,
    kind           TEXT NOT NULL,     -- server, client, internal, producer, consumer
    started_at     TIMESTAMPTZ NOT NULL,
    duration_us    BIGINT NOT NULL,
    error          BOOLEAN NOT NULL,
    status_message TEXT NOT NULL,
    attributes     JSONB NOT NULL,
    events         JSONB NOT NULL
) PARTITION BY RANGE (started_at);
CREATE INDEX IF NOT EXISTS spans_trace ON spans (trace_id);
