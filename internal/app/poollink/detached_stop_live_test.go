package poollink

import (
	"context"
	"net/url"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/warmbly/warmbly/internal/app/email"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/infrastructure/db"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// lifecycleEmails runs the mailbox lifecycle against the real repository.
type lifecycleEmails struct {
	email.EmailService
	repo repository.EmailRepository
}

func (e lifecycleEmails) SetWarmupLifecycle(ctx context.Context, org, id, action string) (*models.Email, *errx.Error) {
	return e.repo.SetWarmupLifecycle(ctx, org, id, action)
}

func TestLiveCloudDetachedLinkWarmupStopsOnce(t *testing.T) {
	dsn := os.Getenv("WARMBLY_TEST_DB")
	if dsn == "" {
		t.Skip("WARMBLY_TEST_DB not set")
	}
	if u, err := url.Parse(dsn); err != nil || (u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1") {
		t.Fatal("requires local PostgreSQL")
	}
	if err := db.RunMigrations(dsn); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql[:min(60, len(sql))], err)
		}
	}

	user, org, revoked, live := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	warming, relinked, local, off, planned := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec(`DELETE FROM admin_settings WHERE key = $1`, repository.PoolLinkDetachedWarmupKey)
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM admin_settings WHERE key = $1`, repository.PoolLinkDetachedWarmupKey)
		_, _ = pool.Exec(c, `DELETE FROM pool_link_instances WHERE organization_id = $1`, org)
		_, _ = pool.Exec(c, `DELETE FROM email_accounts WHERE organization_id = $1`, org)
		_, _ = pool.Exec(c, `DELETE FROM organizations WHERE id = $1`, org)
		_, _ = pool.Exec(c, `DELETE FROM users WHERE id = $1`, user)
	})
	exec(`INSERT INTO users (id, email, first_name, last_name, password_hash) VALUES ($1, $2, 'Detached', 'Link', 'x')`, user, "dl-"+user.String()[:8]+"@test.local")
	exec(`INSERT INTO organizations (id, name, slug, owner_user_id) VALUES ($1, 'Detached Link', $2, $3)`, org, "dl-"+org.String()[:8], user)
	exec(`INSERT INTO pool_link_instances (id, organization_id, name, version, token_hash, revoked_at) VALUES ($1, $2, 'gone', 'v1', $3, NOW() - INTERVAL '1 day')`, revoked, org, "hash-"+revoked.String())
	exec(`INSERT INTO pool_link_instances (id, organization_id, name, version, token_hash) VALUES ($1, $2, 'live', 'v1', $3)`, live, org, "hash-"+live.String())
	for _, id := range []uuid.UUID{warming, relinked, local, off, planned} {
		mode := "'diagnostic'"
		switch id {
		case off:
			mode = "'off'"
		case planned:
			mode = "NULL"
		}
		on := id != off
		exec(`INSERT INTO email_accounts (id, user_id, organization_id, email, name, signature_plain, signature_html, provider, status, timezone, warmup, test_mode, test_send_enabled, test_receive_enabled)
			VALUES ($1, $2, $3, $4, 'DL', '', '', 'gmail', 'active', 'UTC', NOW(), `+mode+`, $5, $5)`, id, user, org, "dl-"+id.String()[:8]+"@test.local", on)
	}
	operation := func(account, plannedAccount any) {
		t.Helper()
		exec(`INSERT INTO pool_link_managed_operations (organization_id, instance_id, remote_id, kind, provider, email_account_id, planned_account_id, consent_state, expires_at)
			VALUES ($1, $2, $3, 'oauth', 'gmail', $4, $5, 'revoked', NOW())`, org, revoked, uuid.New(), account, plannedAccount)
	}
	operation(warming, nil)
	operation(relinked, nil)
	operation(off, nil)
	operation(nil, planned)
	exec(`INSERT INTO pool_link_mailboxes (instance_id, remote_id, email_account_id, managed) VALUES ($1, $2, $3, true)`, live, uuid.New(), relinked)

	emails := repository.NewEmailRepostory(&db.DB{Pool: pool}, nil)
	s := &service{repo: repository.NewPoolLinkRepository(pool, func(ctx context.Context) (*pgx.Conn, error) { return pgx.ConnectConfig(ctx, pool.Config().ConnConfig) }), emailSvc: lifecycleEmails{repo: emails}}
	participating := func(id uuid.UUID) bool {
		t.Helper()
		acc, xerr := emails.GetByID(ctx, id)
		if xerr != nil {
			t.Fatal(xerr)
		}
		return acc.TestSendingAllowed() || acc.TestReceivingAllowed()
	}

	// The first pass fences the rollout; backdate it so this pass completes the stop.
	if err := s.StopDetachedWarmup(ctx); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE admin_settings SET value = jsonb_build_object('fenced_at', to_jsonb(NOW() - INTERVAL '1 hour')) WHERE key = $1`, repository.PoolLinkDetachedWarmupKey)
	if err := s.StopDetachedWarmup(ctx); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[uuid.UUID]bool{warming: false, planned: false, relinked: true, local: true, off: false} {
		if got := participating(id); got != want {
			t.Fatalf("mailbox %v participating = %v, want %v", id, got, want)
		}
	}
	var done bool
	if err := pool.QueryRow(ctx, `SELECT value ? 'done_at' FROM admin_settings WHERE key = $1`, repository.PoolLinkDetachedWarmupKey).Scan(&done); err != nil || !done {
		t.Fatal("a complete pass after the rollout window did not record completion", err)
	}

	// An owner who restarts warmup on Cloud afterwards keeps it.
	if _, xerr := emails.SetWarmupLifecycle(ctx, org.String(), warming.String(), "start"); xerr != nil {
		t.Fatal(xerr)
	}
	if err := s.StopDetachedWarmup(ctx); err != nil {
		t.Fatal(err)
	}
	if !participating(warming) {
		t.Fatal("a completed stop stopped a mailbox its owner restarted")
	}
}
