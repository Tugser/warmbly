package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
)

// An enrollment commits before a revoke of its link or is refused after it, never in between.
func TestLiveCloudEnrollmentRefusesRevokedLink(t *testing.T) {
	_, pool := liveContactDB(t)
	f := newSharedOrgFixture(t, pool)
	ctx := context.Background()
	repo := NewPoolLinkRepository(pool, testSessionConnector(pool))

	instance := uuid.New()
	accounts := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	if _, err := pool.Exec(ctx, `INSERT INTO pool_link_instances (id, organization_id, name, version, token_hash) VALUES ($1, $2, 'self-hosted', 'v1', $3)`,
		instance, f.org, "hash-"+instance.String()); err != nil {
		t.Fatal(err)
	}
	for _, id := range accounts {
		if _, err := pool.Exec(ctx, `INSERT INTO email_accounts (id, user_id, organization_id, email, name, signature_plain, signature_html, provider, status, timezone)
			VALUES ($1, $2, $3, $4, 'Late', '', '', 'smtp_imap', 'active', 'UTC')`, id, f.owner, f.org, uuid.NewString()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM pool_link_mailboxes WHERE instance_id = $1`, instance)
		_, _ = pool.Exec(c, `DELETE FROM email_accounts WHERE id = ANY($1)`, accounts)
		_, _ = pool.Exec(c, `DELETE FROM pool_link_instances WHERE id = $1`, instance)
	})
	enroll := func(account uuid.UUID) error {
		return repo.EnrollMailbox(ctx, &models.PoolLinkMailbox{InstanceID: instance, RemoteID: uuid.New(), EmailAccountID: account})
	}

	if err := enroll(accounts[0]); err != nil {
		t.Fatalf("enrollment on a live link: %v", err)
	}

	// A revoke in flight holds the instance row; the enrollment waits for it and then sees the revoked link.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE pool_link_instances SET revoked_at = NOW() WHERE id = $1`, instance); err != nil {
		t.Fatal(err)
	}
	racing := make(chan error, 1)
	go func() { racing <- enroll(accounts[1]) }()
	select {
	case err := <-racing:
		t.Fatalf("enrollment did not wait for the revoke in flight: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-racing; !errors.Is(err, ErrPoolLinkInstanceRevoked) {
		t.Fatalf("enrollment racing the revoke = %v, want refused", err)
	}

	if err := enroll(accounts[2]); !errors.Is(err, ErrPoolLinkInstanceRevoked) {
		t.Fatalf("enrollment on a revoked link = %v, want refused", err)
	}
	list, err := repo.ListMailboxes(ctx, instance)
	if err != nil || len(list) != 1 || list[0].EmailAccountID != accounts[0] {
		t.Fatalf("enrollments = %+v (%v), want only the one made while the link was live", list, err)
	}
}
