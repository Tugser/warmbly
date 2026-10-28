DROP TABLE IF EXISTS pool_link_self_execution_grants;
DROP TABLE IF EXISTS organization_plan_offers;
-- Kept while any subscription pays for it or holds it as a managed grant; deleting would null the grant into a product plan.
DELETE FROM plans p
WHERE p.id = '00000000-0000-0000-0000-000000000003'
  AND NOT EXISTS (SELECT 1 FROM subscriptions s WHERE s.plan_id = p.id OR s.managed_plan_id = p.id);
