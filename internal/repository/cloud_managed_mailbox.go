package repository

import (
	"context"
	"errors"

	"github.com/warmbly/warmbly/internal/models"
)

// CloudManagedMailboxAdopter keeps an existing local mailbox's identity when adopting its Cloud copy.
type CloudManagedMailboxAdopter interface {
	ConvertManagedMailbox(context.Context, *models.CloudManagedConsent, string) error
}

func (r *cloudLinkRepository) ConvertManagedMailbox(ctx context.Context, c *models.CloudManagedConsent, address string) error {
	if c.Kind != "adopt" || c.InstanceID == nil || c.RemoteID == nil || c.PlannedAccountID == nil || c.CloudAccountID == nil {
		return errors.New("managed adoption identity unavailable")
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var authorized bool
	err = tx.QueryRow(ctx, `SELECT true FROM cloud_managed_consents c
 JOIN email_accounts a ON a.id=c.planned_account_id AND a.organization_id=c.organization_id
 JOIN organizations o ON o.id=c.organization_id AND o.risk_state IN ('trusted','watch')
 JOIN cloud_link l ON l.instance_id=c.instance_id AND (l.organization_id=o.id OR l.organization_id IS NULL) AND NOT l.disconnect_pending
 WHERE c.id=$1 AND c.organization_id=$2 AND c.instance_id=$3 AND c.remote_id=$4
 AND c.planned_account_id=$5 AND c.cloud_account_id=$6 AND c.provider=$7 AND c.kind='adopt'
 AND c.consent_state IN ('pending','unknown') AND a.status='active' AND lower(a.email)=lower($8)
 AND a.provider='smtp_imap' AND a.domain_grant_id IS NULL AND a.vendor_connection_id IS NULL
 AND (o.owner_user_id=c.user_id OR EXISTS (SELECT 1 FROM organization_members m
 WHERE m.organization_id=o.id AND m.user_id=c.user_id AND m.accepted_at IS NOT NULL AND (m.permissions::integer & $9) <> 0))
 FOR UPDATE OF c, a`, c.ID, c.OrganizationID, c.InstanceID, c.RemoteID, c.PlannedAccountID, c.CloudAccountID,
		c.Provider, address, int(models.PermManageEmails)).Scan(&authorized)
	if err != nil {
		return err
	}
	// Replace only an unconfirmed enrollment on this connection, never an active or removing one.
	tag, err := tx.Exec(ctx, `INSERT INTO cloud_link_mailboxes (email_account_id, remote_id, instance_id, managed)
 VALUES ($1,$2,$3,true) ON CONFLICT (email_account_id) DO UPDATE
 SET remote_id=EXCLUDED.remote_id, managed=true, enrollment_state='active', standing_observed_at=NULL, settings_observed_at=NULL
 WHERE cloud_link_mailboxes.instance_id=EXCLUDED.instance_id AND NOT cloud_link_mailboxes.managed
 AND cloud_link_mailboxes.enrollment_state='pending_enroll' AND cloud_link_mailboxes.smtp_cloud_account_id IS NULL`, c.PlannedAccountID, c.RemoteID, c.InstanceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("local mailbox already associated")
	}
	if _, err := tx.Exec(ctx, `UPDATE email_accounts SET provider=$2::email_provider, auth_method='oauth',
 mail_host=CASE WHEN $2::email_provider='gmail' THEN 'google_workspace' ELSE 'microsoft365' END,
 last_id=NULL, transport_generation=gen_random_uuid(), updated_at=now() WHERE id=$1 AND organization_id=$3`,
		c.PlannedAccountID, c.Provider, c.OrganizationID); err != nil {
		return err
	}
	for _, query := range []string{
		`DELETE FROM email_accounts_oauth WHERE email_account_id=$1`,
		`DELETE FROM email_accounts_smtp_imap WHERE email_account_id=$1`,
		`DELETE FROM email_history_ids WHERE email_id=$1`,
		`DELETE FROM email_delta_links WHERE email_id=$1`,
		`INSERT INTO email_sync_state (email_id,user_id,backfill_status,backfill_completed_at)
 SELECT id,user_id,'complete',now() FROM email_accounts WHERE id=$1
 ON CONFLICT (email_id) DO UPDATE SET backfill_status='complete',backfill_cursor='{}'::jsonb,
 backfill_completed_at=COALESCE(email_sync_state.backfill_completed_at,now()),updated_at=now()`,
	} {
		if _, err := tx.Exec(ctx, query, c.PlannedAccountID); err != nil {
			return err
		}
	}
	// Consent and conversion commit together so recovery always preserves the retained mailbox.
	if err := setManagedConsentState(ctx, tx, c.OrganizationID, c.ID, "active", c.PlannedAccountID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
