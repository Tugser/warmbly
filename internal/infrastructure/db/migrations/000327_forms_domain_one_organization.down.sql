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
    ) THEN
        RAISE EXCEPTION 'tracking domain is verified by another organization'
            USING ERRCODE = 'unique_violation', CONSTRAINT = 'tracking_domain_one_organization';
    END IF;
    RETURN NEW;
END;
$$;

DROP INDEX IF EXISTS public.organizations_forms_domain_one_organization;
