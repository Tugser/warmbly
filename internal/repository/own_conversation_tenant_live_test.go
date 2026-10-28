package repository

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// A reply addressed to an inbox is claimed only by that inbox's own workspace: the same address in another workspace has no claim on it.
func TestLiveOwnConversationDoesNotCrossWorkspaces(t *testing.T) {
	handle, pool := liveContactDB(t)
	f := newReplyInboxFixture(t, func(inbox string) string { return inbox })
	ctx := context.Background()
	stranger, strangerOrg, lookalike := uuid.New(), uuid.New(), uuid.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO users (id, first_name, last_name, email, password_hash) VALUES ($1, 'Other', 'Org', $2, 'x')`,
			[]any{stranger, "other-" + stranger.String()[:8] + "@test.local"}},
		{`INSERT INTO organizations (id, name, slug, owner_user_id) VALUES ($1, 'Other Org', $2, $3)`,
			[]any{strangerOrg, "other-" + strangerOrg.String()[:8], stranger}},
		{`INSERT INTO email_accounts (id, user_id, organization_id, email, name,
		      signature_plain, signature_html, provider, status, campaign_limit, min_wait_time, timezone)
		  VALUES ($1, $2, $3, $4, 'Lookalike', '', '', 'smtp_imap', 'active', 50, 0, 'UTC')`,
			[]any{lookalike, stranger, strangerOrg, f.inboxEmail}},
	} {
		if _, err := pool.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	repo := NewEmailSyncStateRepository(handle)
	if own, err := repo.IsOwnConversation(ctx, f.owner, f.inbox, []string{f.messageID}, ""); err != nil || !own {
		t.Fatalf("the reply inbox's own workspace: own=%v err=%v; want true", own, err)
	}
	if own, err := repo.IsOwnConversation(ctx, stranger, lookalike, []string{f.messageID}, ""); err != nil || own {
		t.Fatalf("the same address in another workspace: own=%v err=%v; want false", own, err)
	}
}
