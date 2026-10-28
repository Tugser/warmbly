ALTER TABLE fleet_nodes
    ADD COLUMN IF NOT EXISTS update_failed text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS env_revision text NOT NULL DEFAULT '';
