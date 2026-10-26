-- How many times a problem happened each day (UTC, "2026-10-26": 3), for
-- the last 31 days: the Problems page draws them as a strip of 30 days.
ALTER TABLE problems ADD COLUMN IF NOT EXISTS days JSONB NOT NULL DEFAULT '{}'::jsonb;
