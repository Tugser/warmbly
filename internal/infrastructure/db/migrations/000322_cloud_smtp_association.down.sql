DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM cloud_link_mailboxes WHERE smtp_cloud_account_id IS NOT NULL) THEN
        RAISE EXCEPTION 'Cloud SMTP associations must remain available; roll back the application without removing this column';
    END IF;
END $$;
ALTER TABLE cloud_link_mailboxes DROP COLUMN smtp_cloud_account_id;
