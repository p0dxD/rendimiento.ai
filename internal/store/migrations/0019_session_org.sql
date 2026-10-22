-- The organization a session works in (people switch with the selector at
-- the top). Organization 1 by default, so sessions of the previous version
-- keep working.
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS org_id BIGINT NOT NULL DEFAULT 1 REFERENCES orgs(id);
