DROP TABLE IF EXISTS pool_link_reporter_trust;
DROP TABLE IF EXISTS pool_link_arrivals;
DROP TABLE IF EXISTS pool_link_commands;
ALTER TABLE pool_link_instances DROP COLUMN IF EXISTS executor_seen_at;
DROP INDEX IF EXISTS idx_pool_link_mailboxes_instance_executed;
ALTER TABLE pool_link_mailboxes DROP CONSTRAINT IF EXISTS pool_link_mailboxes_executor_check;
ALTER TABLE pool_link_mailboxes DROP COLUMN IF EXISTS executor;
