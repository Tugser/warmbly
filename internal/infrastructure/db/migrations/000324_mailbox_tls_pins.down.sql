ALTER TABLE email_accounts_smtp_imap
    DROP CONSTRAINT IF EXISTS email_accounts_smtp_imap_tls_pin_check;

ALTER TABLE email_accounts_smtp_imap
    DROP COLUMN IF EXISTS imap_tls_pin,
    DROP COLUMN IF EXISTS imap_tls_unverified,
    DROP COLUMN IF EXISTS smtp_tls_pin,
    DROP COLUMN IF EXISTS smtp_tls_unverified;
