package repository

import (
	"testing"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/infrastructure/db"
)

// A stopped mailbox, as a Cloud link release leaves it, is no recipient for a new send, a reply-back or queued work.
func TestLiveStoppedRecipientIsNeverAddressed(t *testing.T) {
	f, _ := lineageFixture(t)
	ctx := t.Context()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := f.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE email_accounts SET test_mode='diagnostic',test_send_enabled=true,test_receive_enabled=true WHERE id IN($1,$2)`, f.sender, f.recipient)
	exec(`INSERT INTO warmup_pool_participants(pool_id,email_account_id)SELECT wp.id,ea.id FROM warmup_pools wp CROSS JOIN email_accounts ea WHERE wp.pool_type='free' AND ea.id IN($1,$2) ON CONFLICT DO NOTHING`, f.sender, f.recipient)
	t.Cleanup(func() {
		_, _ = f.pool.Exec(ctx, `DELETE FROM warmup_pool_participants WHERE email_account_id IN($1,$2)`, f.sender, f.recipient)
	})

	reply, fresh := uuid.New(), uuid.New()
	exec(`INSERT INTO tasks(id,task_type,email_account_id,status,message_id,scheduled_at)VALUES($1,'warmup',$3,'pending','',NOW()+INTERVAL '1 hour'),($2,'warmup',$3,'pending','',NOW()+INTERVAL '1 hour')`, reply, fresh, f.sender)
	exec(`INSERT INTO warmup_tasks(task_id,target_account_id,parent_task_id,lineage_version,subject,scenario_version,rendering_version,max_turns)VALUES($1,$2,$3,1,'Simulated diagnostic: sample count','diagnostic-v1','canonical-v1',3)`, reply, f.recipient, f.task)
	exec(`INSERT INTO warmup_tokens(token,task_id,sender_account_id,recipient_account_id,subject)VALUES($1,$2,$3,$4,'Simulated diagnostic: sample count')`, uuid.New(), fresh, f.sender, f.recipient)

	receives := func() bool {
		t.Helper()
		ok, err := f.warmup.IsPoolEligible(ctx, f.recipient, "free", false)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	offered := func() bool {
		t.Helper()
		candidates, err := f.warmup.WarmupPartnerCandidates(ctx, "free", f.sender)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range candidates {
			if c.ID == f.recipient {
				return true
			}
		}
		return false
	}
	if !receives() || !offered() {
		t.Fatal("fixture recipient is not eligible before the stop")
	}

	emails := NewEmailRepostory(&db.DB{Pool: f.pool}, nil)
	if _, xerr := emails.SetWarmupLifecycle(ctx, f.org.String(), f.recipient.String(), "stop"); xerr != nil {
		t.Fatal(xerr)
	}

	if receives() {
		t.Fatal("a stopped mailbox still passes the recipient gate used by reply-backs and dispatch")
	}
	if offered() {
		t.Fatal("a stopped mailbox was offered as a new warmup recipient")
	}
	for _, task := range []uuid.UUID{reply, fresh} {
		var status string
		var tokens int
		if err := f.pool.QueryRow(ctx, `SELECT status,(SELECT COUNT(*) FROM warmup_tokens WHERE task_id=$1) FROM tasks WHERE id=$1`, task).Scan(&status, &tokens); err != nil {
			t.Fatal(err)
		}
		if status != "cancelled" || tokens != 0 {
			t.Fatalf("queued send %v to the stopped mailbox is %s with %d tokens, want cancelled with none", task, status, tokens)
		}
	}
}
