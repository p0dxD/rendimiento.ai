-- The diagnostician agent's answers: why a run failed and how to fix it,
-- one per run (asking again replaces it). An organization turns automatic
-- diagnosis off in its AI settings.
CREATE TABLE IF NOT EXISTS run_diagnoses (
    run_id      BIGINT PRIMARY KEY REFERENCES runs(id) ON DELETE CASCADE,
    status      TEXT NOT NULL CHECK (status IN ('pending', 'done', 'failed')),
    text        TEXT NOT NULL DEFAULT '',
    provider    TEXT NOT NULL DEFAULT '',
    model       TEXT NOT NULL DEFAULT '',
    cost_micros BIGINT NOT NULL DEFAULT 0,
    note        TEXT NOT NULL DEFAULT '',   -- why it fell back, or the error
    asked_by    TEXT NOT NULL DEFAULT '',   -- '' when automatic
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE ai_settings ADD COLUMN IF NOT EXISTS auto_diagnose BOOLEAN NOT NULL DEFAULT true;
