-- Release tasks run before the rollout (pre-deploy) or after it (post-deploy).
-- A release whose pre-deploy task failed is recorded with verify_status
-- 'blocked': it was never deployed.
ALTER TABLE release_tasks ADD COLUMN stage TEXT NOT NULL DEFAULT 'post-deploy';
