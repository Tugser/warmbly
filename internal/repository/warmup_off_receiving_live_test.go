package repository

import (
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
)

func offRecipientIsCandidate(t *testing.T, f *poolLinkFixture) bool {
	t.Helper()
	cands, err := f.warmup.WarmupPartnerCandidates(t.Context(), "free", f.sender)
	if err != nil {
		t.Fatal(err)
	}
	return slices.ContainsFunc(cands, func(c models.WarmupPartnerCandidate) bool { return c.ID == f.recipient })
}

// A mailbox already sitting in the state pausing used to leave (paused, diagnostic,
// receive on) is refused at every recipient gate with no backfill, and an explicit
// receive-only choice on a mailbox whose warmup is on still receives.
func TestLiveWarmupOffMailboxReceivesNothing(t *testing.T) {
	f, r, worker, nonce := newWarmupChargeFixture(t, false)
	ctx := t.Context()
	var address string
	if err := f.pool.QueryRow(ctx, `SELECT email FROM email_accounts WHERE id=$1`, f.recipient).Scan(&address); err != nil {
		t.Fatal(err)
	}
	if !offRecipientIsCandidate(t, f) {
		t.Fatal("fixture recipient is not a candidate while warming")
	}

	if _, err := f.pool.Exec(ctx, `UPDATE email_accounts SET warmup_paused_at=NOW(),test_mode='diagnostic',test_send_enabled=false,test_receive_enabled=true WHERE id=$1`, f.recipient); err != nil {
		t.Fatal(err)
	}
	if offRecipientIsCandidate(t, f) {
		t.Fatal("paused mailbox offered as a warmup recipient")
	}
	if ok, err := f.warmup.IsPoolEligible(ctx, f.recipient, "free", false); err != nil || ok {
		t.Fatal("paused mailbox eligible to receive a reply-back", ok, err)
	}
	// The pool row and its standing stay, so a refusal reads as ineligible, not as not_in_pool.
	if h, err := f.warmup.GetParticipantHealth(ctx, f.recipient, "free"); err != nil || h == nil {
		t.Fatal("pausing removed the standing", h, err)
	}
	if _, err := r.ReserveOutbound(ctx, OutboundReservation{TaskID: f.task, MailboxID: f.sender, OrganizationID: f.org, WorkerID: worker, Provider: models.InboxProviderSMTPIMAP, Recipients: []string{address}}); !errors.Is(err, ErrSendAdmissionDenied) {
		t.Fatal("reservation to a paused mailbox was not refused", err)
	}
	state, err := r.BeginWarmupDispatch(ctx, f.task, f.sender, worker, nonce)
	if err != nil || state.State == "execute" {
		t.Fatal("dispatch to a paused mailbox started", state, err)
	}
	reply := uuid.New()
	if _, err := f.pool.Exec(ctx, `INSERT INTO tasks(id,email_account_id,task_type,status,message_id) VALUES($1,$2,'warmup','active','')`, reply, f.sender); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO warmup_tasks(task_id,target_account_id,lineage_version,subject,scenario_version,rendering_version,max_turns) VALUES($1,$2,1,'Pinned diagnostic','diagnostic-v1','canonical-v1',3)`, reply, f.recipient); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO warmup_tokens(token,task_id,sender_account_id,recipient_account_id,subject,content_source,conversation_id,conversation_turn) VALUES($1,$2,$3,$4,'Pinned diagnostic','static',$5,0)`, uuid.New(), reply, f.sender, f.recipient, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AuthorizeWarmupDispatch(ctx, reply, f.sender, worker); err == nil {
		t.Fatal("dispatch authorized to a paused mailbox")
	}

	if _, err := f.pool.Exec(ctx, `UPDATE email_accounts SET warmup_paused_at=NULL WHERE id=$1`, f.recipient); err != nil {
		t.Fatal(err)
	}
	if !offRecipientIsCandidate(t, f) {
		t.Fatal("explicit receive-only mailbox with warmup on stopped receiving")
	}
	if ok, err := f.warmup.IsPoolEligible(ctx, f.recipient, "free", false); err != nil || !ok {
		t.Fatal("explicit receive-only mailbox refused as a recipient", ok, err)
	}
	if _, err := r.AuthorizeWarmupDispatch(ctx, reply, f.sender, worker); err != nil {
		t.Fatal("explicit receive-only mailbox refused at dispatch", err)
	}
}

func TestLiveWarmupPauseRetiresIncomingWork(t *testing.T) {
	f, _, _, _, emails := stopFixture(t, 2)
	row, xerr := emails.SetWarmupLifecycle(t.Context(), f.org.String(), f.recipient.String(), "pause")
	if xerr != nil || row.WarmupReceivingAllowed() {
		t.Fatal("pause left the mailbox receiving", row, xerr)
	}
	var status string
	var tokens int
	if err := f.pool.QueryRow(t.Context(), `SELECT t.status,(SELECT COUNT(*) FROM warmup_tokens WHERE task_id=t.id) FROM tasks t WHERE t.id=$1`, f.task).Scan(&status, &tokens); err != nil || status != "cancelled" || tokens != 0 {
		t.Fatal("pause did not retire the unstarted send to the mailbox", status, tokens, err)
	}
}
