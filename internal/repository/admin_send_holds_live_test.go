package repository

import (
	"testing"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
)

func TestLiveMonitoringSendHoldsList(t *testing.T) {
	handle, pool := liveContactDB(t)
	requireSchemaVersion(t, pool, 277)
	f := newWarmupUsageFixture(t, pool)
	r := NewAdminSendsRepository(handle)
	ctx := t.Context()
	listed := func() *models.AdminSendHold {
		t.Helper()
		res, err := r.SendHolds(ctx, 500)
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range res.Data {
			if h.EmailAccountID == f.account {
				return &h
			}
		}
		return nil
	}
	task := uuid.New()
	f.exec(`INSERT INTO tasks(id,task_type,email_account_id,status,message_id,send_reserved_at,send_result_state,send_recipients)
		VALUES($1,'campaign',$2,'active','',NOW()-INTERVAL '1 minute','unknown',ARRAY['lead@example.test'])`, task, f.account)
	f.exec(`UPDATE email_accounts SET send_recovery_hold=true,send_recovery_reason='unknown',send_recovery_task_id=$2 WHERE id=$1`, f.account, task)
	if h := listed(); h != nil {
		t.Fatal("a send in progress was listed as a hold", h)
	}
	if h, err := r.GetSendHold(ctx, f.account); err != nil || h == nil || h.Resolvable || h.Blocker != models.SendHoldBlockerTaskNotSettled {
		t.Fatal("a running send was offered for resolution", h, err)
	}
	f.exec(`UPDATE tasks SET status='completed',send_reserved_at=NOW()-INTERVAL '20 minutes' WHERE id=$1`, task)
	h := listed()
	if h == nil || !h.Resolvable || h.Reason != "unknown" || h.HeldTaskID == nil || *h.HeldTaskID != task ||
		len(h.Recipients) != 1 || h.OrganizationID == nil || *h.OrganizationID != f.org || h.MailboxStatus != "active" {
		t.Fatal("a settled unknown send was not offered for resolution", h)
	}
	f.exec(`UPDATE email_accounts SET status='inactive' WHERE id=$1`, f.account)
	if h := listed(); h == nil || h.Resolvable || h.Blocker != models.SendHoldBlockerMailboxInactive {
		t.Fatal("a switched-off mailbox was offered for resolution", h)
	}
	// A hold with no recorded send must still list, as task_missing, rather than fail the scan.
	f.exec(`UPDATE email_accounts SET status='active',send_recovery_reason='permanent',send_recovery_task_id=NULL WHERE id=$1`, f.account)
	if h := listed(); h == nil || h.Resolvable || h.HeldTaskID != nil || h.Blocker != models.SendHoldBlockerTaskMissing {
		t.Fatal("a hold without a recorded send was not listed as task_missing", h)
	}
	if h, err := r.GetSendHold(ctx, f.account); err != nil || h == nil || h.Blocker != models.SendHoldBlockerTaskMissing {
		t.Fatal("a hold without a recorded send failed the lookup", h, err)
	}
	f.exec(`UPDATE email_accounts SET send_recovery_hold=false,send_recovery_reason=NULL WHERE id=$1`, f.account)
	if h, err := r.GetSendHold(ctx, f.account); err != nil || h != nil {
		t.Fatal("a released mailbox still reads as held", h, err)
	}
}
