package poollink

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/app/email"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

type mailboxRemovalRepo struct {
	*managedOpsRepo
	trace     *[]string
	deleteErr error
	revoked   bool
}

func (r *mailboxRemovalRepo) DeleteMailbox(ctx context.Context, instance, remote uuid.UUID) error {
	*r.trace = append(*r.trace, "unlink")
	if r.deleteErr != nil {
		return r.deleteErr
	}
	return r.managedOpsRepo.DeleteMailbox(ctx, instance, remote)
}

func (r *mailboxRemovalRepo) RevokeInstance(context.Context, uuid.UUID) error {
	*r.trace = append(*r.trace, "revoke")
	now := time.Now()
	r.instance.RevokedAt = &now
	r.revoked = true
	return nil
}

func (r *mailboxRemovalRepo) ListMailboxes(context.Context, uuid.UUID) ([]models.PoolLinkMailbox, error) {
	if r.mailbox == nil {
		return nil, nil
	}
	return []models.PoolLinkMailbox{*r.mailbox}, nil
}

type mailboxRemovalEmails struct {
	email.EmailService
	account *models.Email
	trace   *[]string
	stopErr *errx.Error
	deleted bool
}

func (e *mailboxRemovalEmails) SetWarmupLifecycle(_ context.Context, org, id, action string) (*models.Email, *errx.Error) {
	if org != e.account.OrganizationID.String() || id != e.account.ID.String() {
		return nil, errx.ErrNotFound
	}
	if action != "stop" {
		panic("removal must stop both directions, not just pause sending")
	}
	*e.trace = append(*e.trace, "stop")
	if e.stopErr != nil {
		return nil, e.stopErr
	}
	off, now := models.TestParticipationOff, time.Now()
	e.account.TestMode = &off
	e.account.TestSendEnabled, e.account.TestReceiveEnabled = false, false
	e.account.WarmupPausedAt = &now
	return e.account, nil
}

func (e *mailboxRemovalEmails) Delete(_ context.Context, org, id string) *errx.Error {
	if org != e.account.OrganizationID.String() || id != e.account.ID.String() {
		return errx.ErrNotFound
	}
	*e.trace = append(*e.trace, "delete")
	e.deleted = true
	return nil
}

func newMailboxRemoval(t *testing.T, managed bool) (*service, *mailboxRemovalRepo, *mailboxRemovalEmails, *[]string) {
	t.Helper()
	org, instance, remote, account := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	trace := &[]string{}
	r := &mailboxRemovalRepo{managedOpsRepo: &managedOpsRepo{
		instance: &models.PoolLinkInstance{ID: instance, OrganizationID: org},
		mailbox:  &models.PoolLinkMailbox{InstanceID: instance, RemoteID: remote, EmailAccountID: account, Managed: managed},
		op:       &models.PoolLinkManagedOperation{State: "completed"},
	}, trace: trace}
	e := &mailboxRemovalEmails{account: &models.Email{ID: account, OrganizationID: &org, Status: "active"}, trace: trace}
	return &service{repo: r, emailSvc: e}, r, e, trace
}

func TestManagedMailboxRemovalStopsBothDirectionsBeforeUnlinking(t *testing.T) {
	s, r, e, trace := newMailboxRemoval(t, true)
	remote := r.mailbox.RemoteID
	if xerr := s.Unenroll(t.Context(), r.instance, remote); xerr != nil {
		t.Fatal(xerr)
	}
	if len(*trace) != 2 || (*trace)[0] != "stop" || (*trace)[1] != "unlink" {
		t.Fatalf("removal order = %v, want stop then unlink", *trace)
	}
	if e.deleted || e.account.Status != "active" || e.account.TestSendingAllowed() || e.account.TestReceivingAllowed() || r.mailbox != nil || r.op.State != "revoked" {
		t.Fatal("removal must retain the Cloud mailbox, stop participation and revoke the mirror")
	}
	if xerr := s.Unenroll(t.Context(), r.instance, remote); xerr != ErrMailboxNotFound || len(*trace) != 2 {
		t.Fatal("retry of a completed removal must not change mailbox state", xerr, *trace)
	}
}

func TestManagedMailboxRemovalKeepsLinkWhenStoppingFails(t *testing.T) {
	s, r, e, trace := newMailboxRemoval(t, true)
	e.stopErr = errx.InternalError()
	if xerr := s.Unenroll(t.Context(), r.instance, r.mailbox.RemoteID); xerr == nil || r.mailbox == nil || len(*trace) != 1 {
		t.Fatal("a failed stop must retain the link for retry", xerr, *trace)
	}
	e.stopErr = nil
	if xerr := s.Unenroll(t.Context(), r.instance, r.mailbox.RemoteID); xerr != nil || r.mailbox != nil || e.account.TestReceivingAllowed() {
		t.Fatal("retry must stop and release the retained link", xerr)
	}
}

func TestManagedMailboxRemovalRemainsStoppedWhenUnlinkFails(t *testing.T) {
	s, r, e, _ := newMailboxRemoval(t, true)
	r.deleteErr = errors.New("database unavailable")
	if xerr := s.Unenroll(t.Context(), r.instance, r.mailbox.RemoteID); xerr == nil || r.mailbox == nil || e.account.TestSendingAllowed() || e.account.TestReceivingAllowed() {
		t.Fatal("a failed unlink must retain retry state without resuming participation", xerr)
	}
	r.deleteErr = nil
	if xerr := s.Unenroll(t.Context(), r.instance, r.mailbox.RemoteID); xerr != nil || r.mailbox != nil || e.deleted {
		t.Fatal("retry must release the link without deleting the Cloud mailbox", xerr)
	}
}

func TestInstanceDisconnectStopsManagedCloudParticipation(t *testing.T) {
	s, r, e, trace := newMailboxRemoval(t, true)
	if xerr := s.RevokeInstance(t.Context(), r.instance.OrganizationID, r.instance.ID); xerr != nil {
		t.Fatal(xerr)
	}
	want := []string{"stop", "revoke", "stop", "unlink"}
	if !slices.Equal(*trace, want) || e.deleted || e.account.TestSendingAllowed() || e.account.TestReceivingAllowed() || r.mailbox != nil || !r.revoked {
		t.Fatalf("disconnect = %v, want %v with the Cloud mailbox kept and both directions stopped", *trace, want)
	}
}

func TestInstanceDisconnectStaysRetryableWhenStopFails(t *testing.T) {
	s, r, e, trace := newMailboxRemoval(t, true)
	e.stopErr = errx.InternalError()
	if xerr := s.RevokeInstance(t.Context(), r.instance.OrganizationID, r.instance.ID); xerr == nil || r.revoked || r.mailbox == nil {
		t.Fatal("a failed stop must keep the link and its mailbox for the instance to retry", xerr, *trace)
	}
	e.stopErr = nil
	if xerr := s.RevokeInstance(t.Context(), r.instance.OrganizationID, r.instance.ID); xerr != nil || !r.revoked || r.mailbox != nil || e.account.TestReceivingAllowed() {
		t.Fatal("retry must stop the mailbox and end the link", xerr, *trace)
	}
}

func TestUnmanagedMailboxRemovalAndInstanceDisconnectDeleteCloudCredential(t *testing.T) {
	for _, disconnect := range []bool{false, true} {
		s, r, e, trace := newMailboxRemoval(t, false)
		var xerr *errx.Error
		want := []string{"stop", "delete", "unlink"}
		if disconnect {
			xerr = s.RevokeInstance(t.Context(), r.instance.OrganizationID, r.instance.ID)
			want = append([]string{"stop", "revoke"}, want...)
		} else {
			xerr = s.Unenroll(t.Context(), r.instance, r.mailbox.RemoteID)
		}
		if xerr != nil || !e.deleted || r.mailbox != nil || !slices.Equal(*trace, want) || e.account.TestReceivingAllowed() {
			t.Fatalf("disconnect=%v: unmanaged removal = %v, %v, want %v", disconnect, *trace, xerr, want)
		}
	}
}
