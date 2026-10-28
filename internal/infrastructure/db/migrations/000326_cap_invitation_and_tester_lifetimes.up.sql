-- Outstanding invitations and reviewer tester passwords live at most 48 hours from now.
-- A re-invite keeps the row's created_at, so the bound is taken from now rather than from it.
UPDATE organization_invitations
   SET expires_at = LEAST(expires_at, now() + interval '48 hours')
 WHERE expires_at > now() + interval '48 hours';

UPDATE users
   SET password_expires_at = LEAST(password_expires_at, now() + interval '48 hours'),
       updated_at = now()
 WHERE password_expires_at > now() + interval '48 hours';
