-- A mailbox server's owner may trust it on a pinned certificate instead of a
-- certificate authority. Expand-only: older releases ignore these columns.
ALTER TABLE email_accounts_smtp_imap
    ADD COLUMN IF NOT EXISTS smtp_tls_unverified boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS smtp_tls_pin text,
    ADD COLUMN IF NOT EXISTS imap_tls_unverified boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS imap_tls_pin text;

ALTER TABLE email_accounts_smtp_imap
    ADD CONSTRAINT email_accounts_smtp_imap_tls_pin_check
    CHECK ((smtp_tls_pin IS NULL OR smtp_tls_pin ~ '^[0-9a-f]{64}$')
       AND (imap_tls_pin IS NULL OR imap_tls_pin ~ '^[0-9a-f]{64}$')) NOT VALID;
