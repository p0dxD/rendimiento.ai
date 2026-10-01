-- Release verification: after a release goes live, its services are watched
-- for a few minutes; a release that breaks them is rolled back.
--   verify_status   '' (from before verification existed), verifying,
--                   passed, failed (rolled back), failed-kept (reported only),
--                   skipped (first release, manual rollback, turned off),
--                   superseded (a newer release came first)
ALTER TABLE releases
    ADD COLUMN verify_status  TEXT NOT NULL DEFAULT '',
    ADD COLUMN verify_message TEXT NOT NULL DEFAULT '',
    ADD COLUMN verified_at    TIMESTAMPTZ;
