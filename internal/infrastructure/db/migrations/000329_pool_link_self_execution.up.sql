-- Credential-free Cloud warmup. A linked instance may keep a mailbox's only
-- credential: Cloud then queues that mailbox's warmup as commands the instance
-- leases and answers, and the instance reports the warmup mail it finds.

-- Which side runs the mailbox's warmup. Existing links stay Cloud-executed.
ALTER TABLE pool_link_mailboxes
    ADD COLUMN IF NOT EXISTS executor text NOT NULL DEFAULT 'cloud';

ALTER TABLE pool_link_mailboxes
    DROP CONSTRAINT IF EXISTS pool_link_mailboxes_executor_check;
ALTER TABLE pool_link_mailboxes
    ADD CONSTRAINT pool_link_mailboxes_executor_check CHECK (executor IN ('cloud', 'instance'));

CREATE INDEX IF NOT EXISTS idx_pool_link_mailboxes_instance_executed
    ON pool_link_mailboxes (email_account_id) WHERE executor = 'instance';

-- When the instance last leased commands; a self-executed mailbox sends only while this is recent.
ALTER TABLE pool_link_instances
    ADD COLUMN IF NOT EXISTS executor_seen_at timestamptz;

-- The per-link command queue. payload is the whole command sealed with the
-- organization's data key, so no warmup content is stored in the clear.
CREATE TABLE IF NOT EXISTS pool_link_commands (
    id               uuid PRIMARY KEY,
    instance_id      uuid NOT NULL REFERENCES pool_link_instances (id) ON DELETE CASCADE,
    email_account_id uuid NOT NULL REFERENCES email_accounts (id) ON DELETE CASCADE,
    organization_id  uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    kind             text NOT NULL CHECK (kind IN ('send', 'action')),
    task_id          uuid REFERENCES tasks (id) ON DELETE CASCADE,
    dispatch_nonce   uuid,
    dedupe_key       text NOT NULL DEFAULT '',
    payload          text NOT NULL,
    status           text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'leased', 'answered', 'expired', 'cancelled')),
    not_after        timestamptz NOT NULL,
    leased_until     timestamptz,
    lease_count      integer NOT NULL DEFAULT 0,
    outcome          text,
    result           jsonb,
    created_at       timestamptz NOT NULL DEFAULT now(),
    answered_at      timestamptz,
    CHECK ((kind = 'send') = (task_id IS NOT NULL AND dispatch_nonce IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS idx_pool_link_commands_open
    ON pool_link_commands (instance_id, created_at) WHERE status IN ('pending', 'leased');
CREATE INDEX IF NOT EXISTS idx_pool_link_commands_expiry
    ON pool_link_commands (not_after) WHERE status IN ('pending', 'leased');
CREATE INDEX IF NOT EXISTS idx_pool_link_commands_account
    ON pool_link_commands (email_account_id);
CREATE INDEX IF NOT EXISTS idx_pool_link_commands_org
    ON pool_link_commands (organization_id);
CREATE INDEX IF NOT EXISTS idx_pool_link_commands_task
    ON pool_link_commands (task_id);
CREATE INDEX IF NOT EXISTS idx_pool_link_commands_closed
    ON pool_link_commands (created_at) WHERE status IN ('answered', 'expired', 'cancelled');
CREATE UNIQUE INDEX IF NOT EXISTS uq_pool_link_commands_open_task
    ON pool_link_commands (task_id) WHERE task_id IS NOT NULL AND status IN ('pending', 'leased');
CREATE UNIQUE INDEX IF NOT EXISTS uq_pool_link_commands_open_dedupe
    ON pool_link_commands (email_account_id, dedupe_key) WHERE dedupe_key <> '' AND status IN ('pending', 'leased');

-- Warmup mail an instance reported and Cloud accepted, by the instance's opaque handle.
CREATE TABLE IF NOT EXISTS pool_link_arrivals (
    email_account_id  uuid NOT NULL REFERENCES email_accounts (id) ON DELETE CASCADE,
    message_handle    text NOT NULL CHECK (message_handle <> ''),
    message_id        text NOT NULL,
    internal_id       uuid NOT NULL,
    sender_account_id uuid REFERENCES email_accounts (id) ON DELETE SET NULL,
    landed_spam       boolean NOT NULL DEFAULT false,
    received_at       timestamptz NOT NULL,
    reported_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (email_account_id, message_handle)
);

-- One accepted arrival per message per mailbox, whatever handle reported it.
CREATE UNIQUE INDEX IF NOT EXISTS uq_pool_link_arrivals_message
    ON pool_link_arrivals (email_account_id, btrim(message_id, '<>'));
CREATE INDEX IF NOT EXISTS idx_pool_link_arrivals_sender
    ON pool_link_arrivals (sender_account_id);
CREATE INDEX IF NOT EXISTS idx_pool_link_arrivals_internal
    ON pool_link_arrivals (email_account_id, internal_id);
CREATE INDEX IF NOT EXISTS idx_pool_link_arrivals_reported
    ON pool_link_arrivals (reported_at);

-- How far a self-executed mailbox's placement reports agree with what Cloud saw for the same senders.
CREATE TABLE IF NOT EXISTS pool_link_reporter_trust (
    email_account_id uuid PRIMARY KEY REFERENCES email_accounts (id) ON DELETE CASCADE,
    organization_id  uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    trust_state      text NOT NULL DEFAULT 'probation'
        CHECK (trust_state IN ('trusted', 'probation', 'untrusted')),
    reported_samples integer NOT NULL DEFAULT 0,
    observed_samples integer NOT NULL DEFAULT 0,
    reported_rate    double precision,
    expected_rate    double precision,
    divergence       double precision,
    evaluated_at     timestamptz NOT NULL DEFAULT now(),
    changed_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_pool_link_reporter_trust_untrusted
    ON pool_link_reporter_trust (email_account_id) WHERE trust_state = 'untrusted';
CREATE INDEX IF NOT EXISTS idx_pool_link_reporter_trust_org
    ON pool_link_reporter_trust (organization_id);
