package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

type CloudSMTPAssociationRepository interface {
	BeginSMTPAssociation(ctx context.Context, accountID, instanceID, cloudAccountID uuid.UUID) error
}

func (r *cloudLinkRepository) BeginSMTPAssociation(ctx context.Context, accountID, instanceID, cloudAccountID uuid.UUID) error {
	tag, err := r.db.Exec(ctx, `INSERT INTO cloud_link_mailboxes
 (email_account_id,remote_id,instance_id,managed,enrollment_state,smtp_cloud_account_id)
 SELECT a.id,gen_random_uuid(),l.instance_id,false,'pending_enroll',$3 FROM email_accounts a JOIN cloud_link l
 ON (l.organization_id=a.organization_id OR l.organization_id IS NULL)
 WHERE a.id=$1 AND a.provider='smtp_imap' AND a.status='active' AND l.instance_id=$2 AND NOT l.disconnect_pending
 ON CONFLICT(email_account_id) DO UPDATE SET smtp_cloud_account_id=EXCLUDED.smtp_cloud_account_id,
 remote_id=CASE WHEN cloud_link_mailboxes.smtp_cloud_account_id IS NULL THEN EXCLUDED.remote_id ELSE cloud_link_mailboxes.remote_id END
 WHERE cloud_link_mailboxes.instance_id=EXCLUDED.instance_id AND NOT cloud_link_mailboxes.managed
 AND cloud_link_mailboxes.enrollment_state='pending_enroll'
 AND (cloud_link_mailboxes.smtp_cloud_account_id IS NULL OR cloud_link_mailboxes.smtp_cloud_account_id=EXCLUDED.smtp_cloud_account_id)`, accountID, instanceID, cloudAccountID)
	if err == nil && tag.RowsAffected() != 1 {
		return errors.New("Cloud SMTP association identity changed")
	}
	return err
}
