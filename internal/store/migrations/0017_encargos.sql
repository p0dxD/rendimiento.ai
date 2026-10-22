-- Encargos: work asked of agents, with an owner and a life cycle. A person
-- (or an agent) creates one; an agent takes it inside a cargo, may wait on
-- something (a run) and hands it over with a result; the person reviews it
-- or sends it back with a comment.
CREATE TABLE IF NOT EXISTS encargos (
    id           BIGSERIAL PRIMARY KEY,
    title        TEXT NOT NULL,
    body         TEXT NOT NULL DEFAULT '',
    app          TEXT NOT NULL DEFAULT '',    -- the app it is about ('' for none)
    run_id       BIGINT,                      -- the run it is about, if any
    status       TEXT NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending', 'taken', 'waiting', 'delivered', 'reviewed', 'cancelled')),
    created_by   TEXT NOT NULL,               -- GitHub login, or the agent key's name
    by_agent     BOOLEAN NOT NULL DEFAULT false,
    key_id       BIGINT REFERENCES agent_keys(id), -- the agent that has it
    cargo_id     BIGINT REFERENCES cargos(id),
    waiting_run  BIGINT,                      -- while waiting: the run it waits for
    result       TEXT NOT NULL DEFAULT '',    -- what the agent handed over
    links        TEXT[] NOT NULL DEFAULT '{}',
    comment      TEXT NOT NULL DEFAULT '',    -- the person's, when sending it back
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS encargos_open ON encargos (status, updated_at DESC);

-- What happened to each encargo, in order: created, taken, waiting, handed
-- over, reviewed, sent back, cancelled.
CREATE TABLE IF NOT EXISTS encargo_log (
    id         BIGSERIAL PRIMARY KEY,
    encargo_id BIGINT NOT NULL REFERENCES encargos(id),
    at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    who        TEXT NOT NULL,
    what       TEXT NOT NULL,   -- created, taken, waiting, resumed, delivered, reviewed, returned, cancelled
    note       TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS encargo_log_encargo ON encargo_log (encargo_id, at);
