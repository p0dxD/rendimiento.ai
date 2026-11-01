-- The language a diagnosis was written in, and its translations
-- ({"en": "…"}): it is diagnosed once and read in either language, each
-- translation made once, by the AI, the first time someone reads it.
ALTER TABLE run_diagnoses ADD COLUMN IF NOT EXISTS lang TEXT NOT NULL DEFAULT '';
ALTER TABLE run_diagnoses ADD COLUMN IF NOT EXISTS translations JSONB NOT NULL DEFAULT '{}'::jsonb;
