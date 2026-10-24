package poollink

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// hostedAccounts holds one SMTP/IMAP mailbox the Cloud workspace connected itself.
type hostedAccounts struct {
	managedOpsEmails
	hosted *models.Email
	ref    models.EmailRef
	creds  repository.SMTPCredentials
}

func (r *hostedAccounts) NewSMTPIMAPAccount(context.Context, string, models.NewSMTPIMAPAccount) (*models.Email, *errx.Error) {
	return nil, errx.ErrEmailOnboardAlreadyExists
}

func (r *hostedAccounts) FindInOrganization(_ context.Context, orgID uuid.UUID, email string) (*models.EmailRef, *errx.Error) {
	if r.hosted.OrganizationID == nil || *r.hosted.OrganizationID != orgID || email != r.hosted.Email {
		return nil, nil
	}
	ref := r.ref
	return &ref, nil
}

func (r *hostedAccounts) GetSMTPCredentials(context.Context, uuid.UUID) (*repository.SMTPCredentials, *errx.Error) {
	c := r.creds
	return &c, nil
}

type hostedLinkRepo struct {
	adoptLinkRepo
	boundElsewhere bool
}

func (r *hostedLinkRepo) GetMailboxByAccount(_ context.Context, accountID uuid.UUID) (*models.PoolLinkMailbox, error) {
	if r.boundElsewhere {
		return &models.PoolLinkMailbox{InstanceID: uuid.New(), RemoteID: uuid.New(), EmailAccountID: accountID}, nil
	}
	return nil, nil
}

type campaignUsage bool

func (c campaignUsage) AccountHasActiveCampaign(context.Context, uuid.UUID) (bool, error) {
	return bool(c), nil
}

func TestEnrollAdoptsTheWorkspacesOwnMailboxOnlyWithItsExactCredentials(t *testing.T) {
	t.Setenv("BILLING_PROVIDER", "none")
	smtp := models.Service{Host: "smtp.example.test", Port: 465, Username: "sender@example.test", Password: "synthetic-a"}
	imap := models.Service{Host: "imap.example.test", Port: 993, Username: "sender@example.test", Password: "synthetic-a"}
	cases := []struct {
		name           string
		mutate         func(*models.SmtpImap, *models.EmailRef)
		campaigns      CampaignUsage
		boundElsewhere bool
		adopt          bool
	}{
		{name: "same credentials", campaigns: campaignUsage(false), adopt: true},
		{name: "host case differs", mutate: func(c *models.SmtpImap, _ *models.EmailRef) { c.IMAP.Host = "IMAP.Example.Test" }, campaigns: campaignUsage(false), adopt: true},
		{name: "different password", mutate: func(c *models.SmtpImap, _ *models.EmailRef) { c.SMTP.Password = "synthetic-b" }, campaigns: campaignUsage(false)},
		{name: "different imap user", mutate: func(c *models.SmtpImap, _ *models.EmailRef) { c.IMAP.Username = "other@example.test" }, campaigns: campaignUsage(false)},
		{name: "different port", mutate: func(c *models.SmtpImap, _ *models.EmailRef) { c.SMTP.Port = 587 }, campaigns: campaignUsage(false)},
		{name: "active campaign sends from it", campaigns: campaignUsage(true)},
		{name: "campaign usage unknown", campaigns: nil},
		{name: "already enrolled by another instance", campaigns: campaignUsage(false), boundElsewhere: true},
		{name: "inactive", mutate: func(_ *models.SmtpImap, r *models.EmailRef) { r.Status = "inactive" }, campaigns: campaignUsage(false)},
		{name: "cloud managed", mutate: func(_ *models.SmtpImap, r *models.EmailRef) { r.Managed = true }, campaigns: campaignUsage(false)},
		{name: "not smtp imap", mutate: func(_ *models.SmtpImap, r *models.EmailRef) { r.Provider = string(models.InboxProviderGoogle) }, campaigns: campaignUsage(false)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			org, owner := uuid.New(), uuid.New()
			hosted := &models.Email{ID: uuid.New(), OrganizationID: &org, Email: "sender@example.test", Provider: "smtp_imap", Status: "active"}
			accounts := &hostedAccounts{
				managedOpsEmails: managedOpsEmails{accounts: map[uuid.UUID]*models.Email{hosted.ID: hosted}},
				hosted:           hosted,
				ref:              models.EmailRef{ID: hosted.ID, Provider: "smtp_imap", Status: "active", AuthMethod: "password"},
				creds: repository.SMTPCredentials{
					SMTPHost: smtp.Host, SMTPPort: smtp.Port, SMTPUser: smtp.Username, SMTPPassword: smtp.Password,
					IMAPHost: imap.Host, IMAPPort: imap.Port, IMAPUser: imap.Username, IMAPPassword: imap.Password,
				},
			}
			presented := &models.SmtpImap{SMTP: new(models.Service), IMAP: new(models.Service)}
			*presented.SMTP, *presented.IMAP = smtp, imap
			if tc.mutate != nil {
				tc.mutate(presented, &accounts.ref)
			}
			r := &hostedLinkRepo{boundElsewhere: tc.boundElsewhere}
			emails := &adoptEmails{org: org}
			s := &service{repo: r, emails: accounts, emailSvc: emails, campaigns: tc.campaigns}
			inst := &models.PoolLinkInstance{ID: uuid.New(), OrganizationID: org, CreatedBy: &owner}
			req := models.PoolLinkEnrollRequest{RemoteID: uuid.New(), Email: hosted.Email, Provider: models.InboxProviderSMTPIMAP, SMTPIMAP: presented,
				Warmup: models.PoolLinkWarmupSettings{Base: 31, Max: 42, Increase: 5}}

			got, xerr := s.Enroll(context.Background(), inst, req)
			if !tc.adopt {
				if got != nil || xerr != ErrMailboxConflict || r.enrolled != nil || emails.started {
					t.Fatalf("must refuse without attaching: %+v, %v", got, xerr)
				}
				return
			}
			if xerr != nil || got == nil {
				t.Fatalf("adoption failed: %v", xerr)
			}
			if r.enrolled == nil || r.enrolled.EmailAccountID != hosted.ID || r.enrolled.InstanceID != inst.ID || r.enrolled.RemoteID != req.RemoteID || r.enrolled.Managed {
				t.Fatalf("adopted enrollment = %+v", r.enrolled)
			}
			if !emails.started {
				t.Fatal("adopted mailbox did not start warmup")
			}
		})
	}
}
