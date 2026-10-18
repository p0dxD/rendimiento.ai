-- Live events too large for a Postgres NOTIFY (8000 bytes): the bridge
-- between the platform's processes stores them here and sends only the id.
-- Rows are needed for seconds; the bridge prunes those older than 10 minutes.
CREATE TABLE IF NOT EXISTS live_events (
    id         BIGSERIAL PRIMARY KEY,
    payload    TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
