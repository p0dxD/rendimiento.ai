-- Opening an encargo for each failed run, once the diagnostician has
-- explained it: off (a person asks with "Ask an agent"), branches (failures
-- off the default branch) or all.
ALTER TABLE ai_settings ADD COLUMN IF NOT EXISTS auto_encargo TEXT NOT NULL DEFAULT 'off'
    CHECK (auto_encargo IN ('off', 'branches', 'all'));
-- An app may choose for itself ('' follows the organization).
ALTER TABLE apps ADD COLUMN IF NOT EXISTS auto_encargo TEXT NOT NULL DEFAULT ''
    CHECK (auto_encargo IN ('', 'off', 'branches', 'all'));
