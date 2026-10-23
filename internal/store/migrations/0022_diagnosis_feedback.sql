-- What people say of a diagnosis, so the diagnostician learns: useful, or
-- wrong with the real cause. Either way it becomes a known failure in the
-- Ruta (ruta_entry), which the diagnostician reads for similar failures.
-- signature is the failure's telling log line.
ALTER TABLE run_diagnoses ADD COLUMN IF NOT EXISTS signature   TEXT NOT NULL DEFAULT '';
ALTER TABLE run_diagnoses ADD COLUMN IF NOT EXISTS feedback    TEXT NOT NULL DEFAULT '' CHECK (feedback IN ('', 'useful', 'wrong'));
ALTER TABLE run_diagnoses ADD COLUMN IF NOT EXISTS feedback_by TEXT NOT NULL DEFAULT '';
ALTER TABLE run_diagnoses ADD COLUMN IF NOT EXISTS ruta_entry  BIGINT REFERENCES ruta_entries(id);
