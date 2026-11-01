-- The platform's coding agent: it does encargos on a branch of the app's
-- repository and tests them with the platform's CI. Off by default; asked
-- does only the ones a person sends it (do_now), auto also takes the easy
-- ones (route platform) on its own. spent_micros is what it spent on each.
ALTER TABLE ai_settings ADD COLUMN IF NOT EXISTS code_agent TEXT NOT NULL DEFAULT 'off'
    CHECK (code_agent IN ('off', 'asked', 'auto'));
ALTER TABLE encargos ADD COLUMN IF NOT EXISTS do_now BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE encargos ADD COLUMN IF NOT EXISTS spent_micros BIGINT NOT NULL DEFAULT 0;
