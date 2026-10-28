-- Which side runs an enrolled mailbox's warmup: Warmbly Cloud with a copy of
-- its credential, or this instance, which keeps the only credential and
-- executes the commands Cloud leases to it.
ALTER TABLE cloud_link_mailboxes ADD COLUMN executor text NOT NULL DEFAULT 'cloud';
ALTER TABLE cloud_link_mailboxes
    ADD CONSTRAINT cloud_link_mailboxes_executor_check CHECK (executor IN ('cloud', 'instance'));

-- A Cloud warmup send the instance executes is a task of its own, so it
-- shares the mailbox's send admission, caps and outcome loop. Postgres cannot
-- drop an enum value, so the down migration leaves it.
ALTER TYPE public.task_type ADD VALUE IF NOT EXISTS 'cloud_warmup';

-- Commands leased from Cloud for instance-executed mailboxes. The payload is
-- sealed with the workspace key; actions is plain so admission can read it.
CREATE TABLE cloud_link_commands (
    id               uuid PRIMARY KEY,
    instance_id      uuid NOT NULL REFERENCES cloud_link (instance_id) ON DELETE CASCADE,
    email_account_id uuid NOT NULL REFERENCES email_accounts (id) ON DELETE CASCADE,
    kind             text NOT NULL CHECK (kind IN ('send', 'action')),
    payload          text NOT NULL,
    actions          text[] NOT NULL DEFAULT '{}',
    message_handle   uuid,
    rfc_message_id   text NOT NULL DEFAULT '',
    not_after        timestamptz NOT NULL,
    received_at      timestamptz NOT NULL DEFAULT NOW(),
    state            text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'dispatched', 'answered')),
    task_id          uuid,
    attempts         integer NOT NULL DEFAULT 0,
    next_attempt_at  timestamptz NOT NULL DEFAULT NOW(),
    last_error       text,
    dispatched_at    timestamptz,
    -- admit_until bounds when the instance's worker may act on the command; admitted_at is when it first did.
    admit_until      timestamptz,
    admitted_at      timestamptz,
    outcome          text,
    answered_at      timestamptz
);
CREATE INDEX idx_cloud_link_commands_due ON cloud_link_commands (instance_id, next_attempt_at) WHERE state = 'pending';
CREATE UNIQUE INDEX idx_cloud_link_commands_task ON cloud_link_commands (task_id) WHERE task_id IS NOT NULL;
CREATE INDEX idx_cloud_link_commands_admission ON cloud_link_commands (email_account_id, admit_until) WHERE admit_until IS NOT NULL;
CREATE INDEX idx_cloud_link_commands_account ON cloud_link_commands (email_account_id, received_at);

-- Durable outbox of what the instance owes Cloud: command results and
-- observations on warmup mail it reported. Retried until Cloud takes them.
CREATE TABLE cloud_link_reports (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    instance_id      uuid NOT NULL REFERENCES cloud_link (instance_id) ON DELETE CASCADE,
    email_account_id uuid REFERENCES email_accounts (id) ON DELETE CASCADE,
    kind             text NOT NULL CHECK (kind IN ('result', 'observation')),
    command_id       uuid UNIQUE,
    remote_id        uuid,
    body             jsonb NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT NOW(),
    attempts         integer NOT NULL DEFAULT 0,
    next_attempt_at  timestamptz NOT NULL DEFAULT NOW(),
    delivered_at     timestamptz,
    last_error       text,
    CHECK ((kind = 'result') = (command_id IS NOT NULL))
);
CREATE INDEX idx_cloud_link_reports_due ON cloud_link_reports (instance_id, next_attempt_at) WHERE delivered_at IS NULL;

-- Warmup mail this instance reported to Cloud, keyed by the opaque handle
-- Cloud echoes back on later actions. Provider coordinates only, no content.
CREATE TABLE cloud_link_warmup_messages (
    handle             uuid PRIMARY KEY,
    email_account_id   uuid NOT NULL REFERENCES email_accounts (id) ON DELETE CASCADE,
    user_id            uuid NOT NULL,
    internal_id        uuid NOT NULL,
    rfc_message_id     text NOT NULL,
    gmail_id           text NOT NULL DEFAULT '',
    uid                bigint NOT NULL DEFAULT 0,
    uid_validity       bigint NOT NULL DEFAULT 0,
    folder_path        text NOT NULL DEFAULT '',
    folder             text NOT NULL DEFAULT '',
    reported_at        timestamptz,
    created_at         timestamptz NOT NULL DEFAULT NOW(),
    UNIQUE (email_account_id, internal_id)
);
CREATE INDEX idx_cloud_link_warmup_messages_rfc ON cloud_link_warmup_messages (email_account_id, rfc_message_id);
CREATE INDEX idx_cloud_link_warmup_messages_created ON cloud_link_warmup_messages (created_at);
