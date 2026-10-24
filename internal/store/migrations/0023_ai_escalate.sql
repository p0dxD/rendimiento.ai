-- Escalating new failures: when the Ruta knows nothing like a failure, the
-- diagnostician may ask a stronger model (the organization's own key, or
-- the platform's within its allowance), at most escalate_limit times a
-- month. Off by default.
ALTER TABLE ai_settings ADD COLUMN IF NOT EXISTS escalate_to TEXT NOT NULL DEFAULT ''
    CHECK (escalate_to IN ('', 'own', 'platform'));
ALTER TABLE ai_settings ADD COLUMN IF NOT EXISTS escalate_limit INT NOT NULL DEFAULT 10;
