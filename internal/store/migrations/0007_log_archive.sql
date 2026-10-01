-- Step logs of finished runs move to object storage (MinIO). The text
-- column is emptied; log_ref is the object's key, log_bytes the log's size.
ALTER TABLE steps
    ADD COLUMN log_ref     TEXT NOT NULL DEFAULT '',
    ADD COLUMN log_bytes   BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN archived_at TIMESTAMPTZ;
CREATE INDEX steps_unarchived ON steps (run_id) WHERE log_ref = '' AND log <> '';
