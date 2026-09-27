-- Runs that finished before steps were closed out with them could keep
-- steps in "running" forever (e.g. a run interrupted by a crash).
UPDATE steps SET
    status = CASE WHEN steps.status = 'running' THEN 'failed' ELSE 'skipped' END,
    message = CASE WHEN steps.status = 'running' THEN 'interrupted' ELSE steps.message END,
    finished_at = COALESCE(steps.finished_at, runs.finished_at, now())
FROM runs
WHERE steps.run_id = runs.id
  AND runs.status IN ('succeeded', 'failed', 'cancelled')
  AND steps.status IN ('pending', 'running');
