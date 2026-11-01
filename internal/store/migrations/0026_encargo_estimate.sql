-- Estimating encargos: when one is created, an agent rates how hard it is
-- (1 to 10) and what it would cost the platform's AI to do it. Under the
-- organization's amount (encargo_auto_micros) it is for the platform's AI;
-- over it, for the person's own agent (a monthly plan, not paid per call),
-- unless the person says otherwise (route).
ALTER TABLE encargos ADD COLUMN IF NOT EXISTS difficulty SMALLINT;
ALTER TABLE encargos ADD COLUMN IF NOT EXISTS estimate_micros BIGINT;
ALTER TABLE encargos ADD COLUMN IF NOT EXISTS estimate_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE encargos ADD COLUMN IF NOT EXISTS estimated_at TIMESTAMPTZ;
ALTER TABLE encargos ADD COLUMN IF NOT EXISTS route TEXT NOT NULL DEFAULT ''
    CHECK (route IN ('', 'platform', 'local'));
ALTER TABLE ai_settings ADD COLUMN IF NOT EXISTS encargo_auto_micros BIGINT NOT NULL DEFAULT 500000;
