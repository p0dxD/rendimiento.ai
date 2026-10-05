-- A problem can be resolved by the platform when it sees proof it is fixed
-- (a later push accepted, a later email sent). It reopens if it happens again.
ALTER TABLE problems ADD COLUMN resolved_at TIMESTAMPTZ;
ALTER TABLE problems ADD COLUMN resolution TEXT NOT NULL DEFAULT '';
