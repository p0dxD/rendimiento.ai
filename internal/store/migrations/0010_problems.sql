-- Problems: the platform's own warnings and errors, for the Problems page.
-- Repeats of the same problem (same level, component, app, message and
-- error, with numbers and IDs blanked) are one row with a count.
CREATE TABLE problems (
    id           BIGSERIAL PRIMARY KEY,
    fingerprint  TEXT NOT NULL UNIQUE,
    level        TEXT NOT NULL,              -- WARN | ERROR
    component    TEXT NOT NULL DEFAULT '',   -- the part of the platform that reported it
    app          TEXT NOT NULL DEFAULT '',   -- the app it concerns, if any
    message      TEXT NOT NULL,
    detail       TEXT NOT NULL DEFAULT '',   -- the latest occurrence's error
    attrs        TEXT NOT NULL DEFAULT '',   -- the latest occurrence's other fields, key=value
    count        INT NOT NULL DEFAULT 1,
    first_at     TIMESTAMPTZ NOT NULL,
    last_at      TIMESTAMPTZ NOT NULL,
    dismissed_at TIMESTAMPTZ                -- dismissed by a user; set back to NULL when it happens again
);
CREATE INDEX problems_last ON problems (last_at DESC);
