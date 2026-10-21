-- What an agent key may do: read (reader), also write the Ruta and run CI
-- (collaborator), or more, later (admin). Apps narrows the platform's tools
-- to some apps (empty: all). can_write stays for the previous version during
-- an update; new keys keep it in step with the role.
ALTER TABLE agent_keys ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'reader'
    CHECK (role IN ('reader', 'collaborator', 'admin'));
ALTER TABLE agent_keys ADD COLUMN IF NOT EXISTS apps TEXT[] NOT NULL DEFAULT '{}';
UPDATE agent_keys SET role = 'collaborator' WHERE can_write AND role = 'reader';
