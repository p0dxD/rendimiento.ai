-- Secrets the platform keeps when there is no Kubernetes to keep them (the
-- GitHub App's credentials, organizations' AI keys): sealed with AES-GCM by
-- a key that lives outside the database (internal/secretos). Only adds.
CREATE TABLE IF NOT EXISTS secretos (
    nombre      TEXT PRIMARY KEY,
    nonce       BYTEA NOT NULL,
    cifrado     BYTEA NOT NULL,
    actualizado TIMESTAMPTZ NOT NULL DEFAULT now()
);
