-- A 300-lead list and warming senders for the isolated rich-seed QA database, never production.
BEGIN;
DO $$
BEGIN
  IF left(current_database(), 11) <> 'warmbly_qa_' THEN
    RAISE EXCEPTION 'campaign estimate proof fixtures require an isolated warmbly_qa_ database';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM organizations WHERE id = '22222222-0000-0000-0000-000000000001') THEN
    RAISE EXCEPTION 'campaign estimate proof fixtures require the rich QA seed';
  END IF;
END $$;

-- Synthetic leads on the reserved .example TLD, so no address can ever be delivered to.
INSERT INTO contacts (id, user_id, organization_id, first_name, last_name, email, company, phone, custom_fields)
SELECT ('86200000-0000-0000-0000-' || lpad(n::text, 12, '0'))::uuid,
       '11111111-0000-0000-0000-000000000001', '22222222-0000-0000-0000-000000000001',
       'Lead', n::text, 'lead' || n || '@agency' || (n % 400) || '.example', 'Agency ' || (n % 400), '', '{}'::jsonb
FROM generate_series(1, 300) AS n
ON CONFLICT DO NOTHING;

INSERT INTO segments (id, organization_id, created_by, name, description, color)
VALUES ('86200000-0000-0000-0000-000000000000', '22222222-0000-0000-0000-000000000001',
        '11111111-0000-0000-0000-000000000001', 'Agencies', 'Synthetic agency leads', '#0284c7')
ON CONFLICT (id) DO NOTHING;

INSERT INTO segment_members (segment_id, contact_id, mode)
SELECT '86200000-0000-0000-0000-000000000000', ('86200000-0000-0000-0000-' || lpad(n::text, 12, '0'))::uuid, 'include'
FROM generate_series(1, 300) AS n
ON CONFLICT DO NOTHING;

-- Any active sender without warmup history gets three weeks of it, so campaign volume is earned from replies.
UPDATE email_accounts SET warmup = now() - interval '21 days'
WHERE organization_id = '22222222-0000-0000-0000-000000000001' AND status = 'active' AND warmup IS NULL;
-- The product tour would cover the page; the flow records the campaign flow, not the tour.
UPDATE users SET product_tour_completed_at = now()
WHERE id = '11111111-0000-0000-0000-000000000001' AND product_tour_completed_at IS NULL;
COMMIT;
