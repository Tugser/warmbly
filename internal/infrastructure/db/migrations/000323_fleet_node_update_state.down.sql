ALTER TABLE fleet_nodes
    DROP COLUMN IF EXISTS env_revision,
    DROP COLUMN IF EXISTS update_failed;
