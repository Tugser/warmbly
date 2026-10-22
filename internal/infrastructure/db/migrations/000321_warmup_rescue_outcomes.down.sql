ALTER TABLE warmup_received DROP COLUMN rescue_outcome, DROP COLUMN rescue_reported_at, DROP COLUMN placement_group, DROP COLUMN placement_host;
ALTER TABLE warmup_placement_daily DROP COLUMN rescue_confirmed, DROP COLUMN rescue_missing, DROP COLUMN rescue_failed;
