DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM cloud_link_mailboxes WHERE executor = 'instance') THEN
        RAISE EXCEPTION 'Mailboxes are warmed on this instance without a Cloud credential; switch them back to Cloud or unenroll them before rolling back this migration';
    END IF;
END $$;
DROP TABLE IF EXISTS cloud_link_warmup_messages;
DROP TABLE IF EXISTS cloud_link_reports;
DROP TABLE IF EXISTS cloud_link_commands;
ALTER TABLE cloud_link_mailboxes DROP CONSTRAINT IF EXISTS cloud_link_mailboxes_executor_check;
ALTER TABLE cloud_link_mailboxes DROP COLUMN IF EXISTS executor;
