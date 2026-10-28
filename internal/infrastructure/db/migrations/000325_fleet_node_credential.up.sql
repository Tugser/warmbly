-- SHA-256 of the per-node credential issued at join; empty until the node joins on a release that issues one.
ALTER TABLE fleet_nodes
    ADD COLUMN IF NOT EXISTS credential_hash text NOT NULL DEFAULT '';
