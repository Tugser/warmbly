package poollink

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

type smtpAssociationRepo struct {
	*managedOpsRepo
	enrollments int
}

type smtpAssociationEmails struct{ *managedOpsEmails }

func (r *smtpAssociationEmails) Get(ctx context.Context, org, id string) (*models.Email, *errx.Error) {
	acc, xerr := r.GetByID(ctx, uuid.MustParse(id))
	if xerr != nil {
		return nil, xerr
	}
	if acc.OrganizationID == nil || acc.OrganizationID.String() != org {
		return nil, errx.ErrNotFound
	}
	return acc, nil
}

func (r *smtpAssociationRepo) RevokeManagedOperations(ctx context.Context, instance uuid.UUID, remote *uuid.UUID) error {
	if remote == nil {
		return r.managedOpsRepo.RevokeManagedOperations(ctx, instance, remote)
	}
	r.op = &models.PoolLinkManagedOperation{InstanceID: &instance, RemoteID: remote, State: "revoked"}
	return nil
}

func (r *smtpAssociationRepo) EnrollMailbox(_ context.Context, mailbox *models.PoolLinkMailbox) error {
	r.enrollments++
	r.mailbox = mailbox
	return nil
}

func (r *smtpAssociationRepo) RetainMailbox(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
	r.mailbox.Managed = true
	return nil
}
func (r *smtpAssociationRepo) GetMailboxByAccount(_ context.Context, id uuid.UUID) (*models.PoolLinkMailbox, error) {
	if r.mailbox != nil && r.mailbox.EmailAccountID == id {
		return r.mailbox, nil
	}
	return nil, nil
}

func TestExplicitSMTPAssociationKeepsIndependentCredentialsWarmupAndHistory(t *testing.T) {
	org, user, id := uuid.New(), uuid.New(), uuid.New()
	r := &smtpAssociationRepo{managedOpsRepo: &managedOpsRepo{instance: &models.PoolLinkInstance{ID: uuid.New(), OrganizationID: org, CreatedBy: &user}}}
	started, paused := time.Now().Add(-28*24*time.Hour), time.Now().Add(-time.Hour)
	acc := &models.Email{ID: id, OrganizationID: &org, Status: "active", Provider: "smtp_imap", AuthMethod: models.MailAuthAppPassword,
		Email: "sender@example.test", Warmup: &started, WarmupPausedAt: &paused, WarmupMax: 42}
	emails := &managedOpsEmails{accounts: map[uuid.UUID]*models.Email{id: acc}}
	e := &managedOpsEmailService{emails: emails}
	s := &service{repo: r, emails: &smtpAssociationEmails{emails}, emailSvc: e, campaigns: campaignUsage(false)}
	req := models.PoolLinkSMTPAssociationRequest{RemoteID: uuid.New(), EmailAccountID: id, Email: acc.Email}
	for range 2 {
		state, xerr := s.AssociateSMTPIMAP(t.Context(), r.instance, req)
		if xerr != nil || state.EmailAccountID != id || !state.Managed {
			t.Fatalf("association: %+v %v", state, xerr)
		}
	}
	if r.enrollments != 1 || e.starts != 0 || e.loads != 0 || acc.Warmup != &started || acc.WarmupPausedAt != &paused || acc.WarmupMax != 42 || acc.AuthMethod != models.MailAuthAppPassword {
		t.Fatal("association changed credentials, warmup or replayed work")
	}
	if _, xerr := s.AccessToken(t.Context(), r.instance, req.RemoteID); xerr != ErrMailboxNotManaged {
		t.Fatalf("SMTP association exposed an OAuth token: %v", xerr)
	}
	if xerr := s.Unenroll(t.Context(), r.instance, req.RemoteID); xerr != nil {
		t.Fatal(xerr)
	}
	if emails.accounts[id] != acc {
		t.Fatal("unlink removed the independently owned Cloud mailbox")
	}
	if _, xerr := s.AssociateSMTPIMAP(t.Context(), r.instance, req); xerr == nil || r.enrollments != 1 {
		t.Fatal("delayed association revived a revoked remote identity")
	}
	req.RemoteID = uuid.New()
	if _, xerr := s.AssociateSMTPIMAP(t.Context(), r.instance, req); xerr != nil || r.enrollments != 2 {
		t.Fatalf("new explicit association could not reconnect the retained mailbox: %v", xerr)
	}
}

func TestExplicitSMTPAssociationRequiresMatchingWorkspaceIdentityAndUnusedMailbox(t *testing.T) {
	for _, refusal := range []string{"other_workspace", "wrong_address", "inactive", "other_instance", "campaign", "oauth"} {
		t.Run(refusal, func(t *testing.T) {
			org, id := uuid.New(), uuid.New()
			r := &smtpAssociationRepo{managedOpsRepo: &managedOpsRepo{instance: &models.PoolLinkInstance{ID: uuid.New(), OrganizationID: org}}}
			acc := &models.Email{ID: id, OrganizationID: &org, Email: "sender@example.test", Status: "active", Provider: "smtp_imap"}
			req := models.PoolLinkSMTPAssociationRequest{RemoteID: uuid.New(), EmailAccountID: id, Email: acc.Email}
			if refusal == "other_workspace" {
				other := uuid.New()
				acc.OrganizationID = &other
			}
			if refusal == "wrong_address" {
				req.Email = "other@example.test"
			}
			if refusal == "inactive" {
				acc.Status = "inactive"
			}
			if refusal == "oauth" {
				acc.Provider = "gmail"
			}
			if refusal == "other_instance" {
				r.mailbox = &models.PoolLinkMailbox{InstanceID: uuid.New(), EmailAccountID: id}
			}
			s := &service{repo: r, emails: &smtpAssociationEmails{&managedOpsEmails{accounts: map[uuid.UUID]*models.Email{id: acc}}}, campaigns: campaignUsage(refusal == "campaign")}
			if _, xerr := s.AssociateSMTPIMAP(t.Context(), r.instance, req); xerr != ErrMailboxConflict || r.enrollments != 0 {
				t.Fatalf("association refusal: %v", xerr)
			}
		})
	}
}
