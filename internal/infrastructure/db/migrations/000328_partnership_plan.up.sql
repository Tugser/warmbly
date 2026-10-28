-- The Partnership plan: the Warmup plan's entitlements (premium pool, no
-- mailbox cap, warming only) at a partner price, sold only to a workspace an
-- operator has offered it to. The operator attaches the Stripe price on the
-- admin plans page; without one the offer shows as unavailable.
INSERT INTO plans (
    id, name, max_contacts, daily_emails, ai_generation, account_limit,
    price, discounted_price, duration_id, savings, public,
    dedicated_workers, daily_campaign_limit,
    max_campaigns, max_active_campaigns, max_team_members, max_email_accounts,
    monthly_credits
) VALUES (
    '00000000-0000-0000-0000-000000000003', 'Partnership', 100, 20, false, 0,
    90, 90, '00000000-0000-0000-0000-0000000000d1', 0, false,
    0, 20,
    2, 1, 5, NULL,
    0
)
ON CONFLICT (id) DO NOTHING;

-- A private plan one workspace may buy, offered by an operator.
CREATE TABLE IF NOT EXISTS organization_plan_offers (
    organization_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    plan_id         uuid NOT NULL REFERENCES plans (id) ON DELETE CASCADE,
    granted_by      uuid REFERENCES users (id) ON DELETE SET NULL,
    reason          text NOT NULL CHECK (btrim(reason) <> ''),
    granted_at      timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz,
    PRIMARY KEY (organization_id, plan_id)
);

CREATE INDEX IF NOT EXISTS idx_organization_plan_offers_plan ON organization_plan_offers (plan_id);

-- An operator's permission for a workspace's linked instances to warm
-- mailboxes in the Cloud pool while keeping every credential themselves.
CREATE TABLE IF NOT EXISTS pool_link_self_execution_grants (
    organization_id uuid PRIMARY KEY REFERENCES organizations (id) ON DELETE CASCADE,
    granted_by      uuid REFERENCES users (id) ON DELETE SET NULL,
    reason          text NOT NULL CHECK (btrim(reason) <> ''),
    granted_at      timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz
);
