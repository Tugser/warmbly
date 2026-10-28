package repository

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

// An invitation grants one membership: accepting it consumes it in the same transaction.
//
//	WARMBLY_TEST_DB=postgres://warmbly:warmbly@localhost:15432/warmbly_dev?sslmode=disable \
//	  go test ./internal/repository/ -run LiveInvitationAccept -v
func TestLiveInvitationAcceptConsumesOnce(t *testing.T) {
	_, pool := liveContactDB(t)
	ctx := context.Background()
	repo := NewOrganizationRepository(pool)

	owner, invitee, org, role := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("fixture %q: %v", sql[:min(60, len(sql))], err)
		}
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM organizations WHERE id = $1`, org)
		_, _ = pool.Exec(c, `DELETE FROM users WHERE id = ANY($1)`, []uuid.UUID{owner, invitee})
	})
	for _, u := range []uuid.UUID{owner, invitee} {
		exec(`INSERT INTO users (id, first_name, last_name, email, password_hash) VALUES ($1, 'Accept', 'Live', $2, 'x')`, u, "accept-"+u.String()[:8]+"@test.local")
	}
	exec(`INSERT INTO organizations (id, name, slug, owner_user_id) VALUES ($1, 'Accept', $2, $3)`, org, "accept-"+org.String()[:8], owner)
	exec(`INSERT INTO organization_roles (id, organization_id, name, permissions) VALUES ($1, $2, 'Member', 1)`, role, org)

	invite := func(expires time.Time) uuid.UUID {
		t.Helper()
		inv := &models.OrganizationInvitation{
			ID: uuid.New(), OrganizationID: org, Email: "accept-" + invitee.String()[:8] + "@test.local", Role: "Member", RoleID: &role,
			Permissions: 1, InvitedBy: owner, Token: uuid.NewString(), ExpiresAt: expires, CreatedAt: time.Now(),
		}
		if err := repo.CreateInvitation(ctx, inv); err != nil {
			t.Fatal(err)
		}
		stored, err := repo.GetInvitationByID(ctx, inv.ID)
		if err != nil || stored == nil {
			t.Fatalf("stored invitation: %v", err)
		}
		return stored.ID
	}
	member := func() *models.OrganizationMember {
		now := time.Now()
		return &models.OrganizationMember{ID: uuid.New(), OrganizationID: org, UserID: invitee, Role: "Member", RoleID: &role,
			Permissions: 1, InvitedBy: &owner, InvitedAt: now, AcceptedAt: &now}
	}

	t.Run("an expired invitation grants nothing and stays", func(t *testing.T) {
		id := invite(time.Now().Add(-time.Minute))
		if err := repo.AcceptInvitation(ctx, id, member(), []uuid.UUID{role}); !errors.Is(err, ErrInvitationInvalid) {
			t.Fatalf("err = %v, want ErrInvitationInvalid", err)
		}
		if m, _ := repo.GetMember(ctx, org, invitee); m != nil {
			t.Fatal("an expired invitation granted a membership")
		}
		if inv, _ := repo.GetInvitationByID(ctx, id); inv == nil {
			t.Fatal("an expired invitation was removed, so it cannot be sent again")
		}
		expired, err := repo.GetExpiredInvitations(ctx, org, 50)
		if err != nil || len(expired) != 1 || expired[0].ID != id {
			t.Fatalf("expired invitations = %v, %v; want the one just expired", expired, err)
		}
		if pending, _ := repo.GetPendingInvitations(ctx, org); len(pending) != 0 {
			t.Fatal("an expired invitation is listed as pending")
		}
		exec(`DELETE FROM organization_invitations WHERE id = $1`, id)
	})

	t.Run("concurrent accepts grant one membership and consume the invitation", func(t *testing.T) {
		id := invite(time.Now().Add(time.Hour))
		var wg sync.WaitGroup
		results := make([]error, 4)
		for i := range results {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				results[i] = repo.AcceptInvitation(ctx, id, member(), []uuid.UUID{role})
			}(i)
		}
		wg.Wait()
		won := 0
		for _, err := range results {
			if err == nil {
				won++
			}
		}
		if won != 1 {
			t.Fatalf("%d accepts succeeded, want exactly 1: %v", won, results)
		}
		if inv, _ := repo.GetInvitationByID(ctx, id); inv != nil {
			t.Fatal("the invitation survived its acceptance")
		}
		if m, _ := repo.GetMember(ctx, org, invitee); m == nil {
			t.Fatal("the accepted invitation granted no membership")
		}
	})

	t.Run("a failed grant leaves the invitation in place", func(t *testing.T) {
		exec(`DELETE FROM organization_members WHERE organization_id = $1 AND user_id = $2`, org, invitee)
		id := invite(time.Now().Add(time.Hour))
		bad := member()
		bad.UserID = uuid.New() // no such user, so the membership insert fails
		if err := repo.AcceptInvitation(ctx, id, bad, []uuid.UUID{role}); err == nil {
			t.Fatal("a membership for a missing user was granted")
		}
		if inv, _ := repo.GetInvitationByID(ctx, id); inv == nil {
			t.Fatal("the invitation was consumed although no membership was granted")
		}
	})
}
