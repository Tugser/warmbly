-- A custom forms domain is verified by at most one organization on an instance,
-- and never by one other than the organization verifying the same host for
-- tracking or an instance-served redirect.

-- Another organization's verified tracking host or redirect keeps the name.
-- The forms domain they typed is kept, unverified.
UPDATE public.organizations o
SET forms_domain_verified = false, forms_domain_verified_at = NULL
WHERE o.forms_domain_verified AND o.forms_domain <> ''
  AND EXISTS (
      SELECT 1 FROM public.email_accounts ea
      WHERE ea.tracking_domain = o.forms_domain AND ea.tracking_domain_verified
        AND ea.organization_id IS DISTINCT FROM o.id
      UNION ALL
      SELECT 1 FROM public.campaigns c
      WHERE c.tracking_domain = o.forms_domain AND c.tracking_domain_verified
        AND c.organization_id IS DISTINCT FROM o.id
      UNION ALL
      SELECT 1 FROM public.domain_redirects r
      WHERE r.verified AND r.served_by = 'instance' AND r.organization_id <> o.id
        AND (r.domain = o.forms_domain OR (r.include_www AND 'www.' || r.domain = o.forms_domain))
  );

-- Where several organizations hold one forms domain, the earliest verification
-- keeps it and the others go back to unverified.
WITH keeper AS (
    SELECT DISTINCT ON (forms_domain) forms_domain, id
    FROM public.organizations
    WHERE forms_domain_verified AND forms_domain <> ''
    ORDER BY forms_domain, forms_domain_verified_at ASC NULLS LAST, created_at ASC, id
)
UPDATE public.organizations o
SET forms_domain_verified = false, forms_domain_verified_at = NULL
FROM keeper k
WHERE o.forms_domain = k.forms_domain
  AND o.forms_domain_verified
  AND o.id <> k.id;

CREATE UNIQUE INDEX IF NOT EXISTS organizations_forms_domain_one_organization
    ON public.organizations (forms_domain)
    WHERE forms_domain_verified AND forms_domain <> '';

-- A tracking host is refused while another organization holds it as its forms domain.
CREATE OR REPLACE FUNCTION public.tracking_domain_one_organization() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    IF NOT NEW.tracking_domain_verified OR NEW.tracking_domain = '' THEN
        RETURN NEW;
    END IF;
    -- Serializes concurrent verifications of one host.
    PERFORM pg_advisory_xact_lock(hashtextextended('tracking_domain:' || NEW.tracking_domain, 0));
    IF EXISTS (
        SELECT 1 FROM public.email_accounts
        WHERE tracking_domain = NEW.tracking_domain AND tracking_domain_verified
          AND organization_id IS DISTINCT FROM NEW.organization_id
        UNION ALL
        SELECT 1 FROM public.campaigns
        WHERE tracking_domain = NEW.tracking_domain AND tracking_domain_verified
          AND organization_id IS DISTINCT FROM NEW.organization_id
        UNION ALL
        SELECT 1 FROM public.organizations
        WHERE forms_domain = NEW.tracking_domain AND forms_domain_verified
          AND id IS DISTINCT FROM NEW.organization_id
    ) THEN
        RAISE EXCEPTION 'tracking domain is verified by another organization'
            USING ERRCODE = 'unique_violation', CONSTRAINT = 'tracking_domain_one_organization';
    END IF;
    RETURN NEW;
END;
$$;
