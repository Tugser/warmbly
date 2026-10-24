package poollink

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

type delegatedTokens struct {
	token *models.PoolLinkAccessToken
	err   *errx.Error
	calls int
}

func (b *delegatedTokens) AccessToken(context.Context, uuid.UUID) (*models.PoolLinkAccessToken, bool, *errx.Error) {
	b.calls++
	return b.token, true, b.err
}

func (*managedOpsRepo) TouchMailboxToken(context.Context, uuid.UUID, uuid.UUID) error { return nil }

func TestDelegatedGoogleAdoptionSupportsLegacyInstanceRequest(t *testing.T) {
	org, user := uuid.New(), uuid.New()
	acc := &models.Email{ID: uuid.New(), OrganizationID: &org, Provider: "gmail", AuthMethod: models.MailAuthDelegated, Status: "active"}
	r := &adoptLinkRepo{}
	e := &adoptEmails{org: org}
	s := &service{repo: r, emails: adoptAccounts{acc: acc}, emailSvc: e}
	inst := &models.PoolLinkInstance{ID: uuid.New(), OrganizationID: org, CreatedBy: &user}
	if _, xerr := s.Adopt(t.Context(), inst, models.PoolLinkAdoptRequest{RemoteID: uuid.New(), EmailAccountID: acc.ID}); xerr != nil {
		t.Fatal(xerr)
	}
	if r.enrolled == nil || r.enrolled.EmailAccountID != acc.ID || !r.enrolled.Managed || !e.started {
		t.Fatal("legacy adoption did not retain the delegated Cloud mailbox")
	}
}

func TestDelegatedGoogleAdoptionBrokersCloudGrantAndPreservesMailbox(t *testing.T) {
	org, user, id, grant := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	r := &managedOpsRepo{instance: &models.PoolLinkInstance{ID: uuid.New(), OrganizationID: org, CreatedBy: &user}}
	started := time.Now().Add(-28 * 24 * time.Hour)
	acc := &models.Email{ID: id, OrganizationID: &org, Status: "active", Provider: "gmail", AuthMethod: models.MailAuthDelegated,
		DomainGrantID: &grant, Email: "sender@example.test", Warmup: &started, WarmupMax: 42}
	emails := &managedOpsEmails{accounts: map[uuid.UUID]*models.Email{id: acc}}
	e := &managedOpsEmailService{emails: emails}
	s := &service{repo: r, emails: emails, emailSvc: e}
	b := &delegatedTokens{token: &models.PoolLinkAccessToken{AccessToken: "fixture", Provider: "gmail", Email: acc.Email, ExpiresAt: time.Now().Add(time.Hour)}}
	s.WireDelegation(b)
	req := models.PoolLinkAdoptRequest{Protocol: models.ManagedConsentProtocol, RemoteID: uuid.New(), EmailAccountID: id}
	for range 2 {
		state, xerr := s.Adopt(t.Context(), r.instance, req)
		if xerr != nil || state.EmailAccountID != id || !state.Managed || state.ConsentCompletedAt == nil {
			t.Fatalf("adoption: %+v %v", state, xerr)
		}
	}
	if e.starts != 1 || acc.AuthMethod != models.MailAuthDelegated || acc.DomainGrantID != &grant || acc.Warmup != &started || acc.WarmupMax != 42 {
		t.Fatal("adoption changed the Cloud mailbox or replayed activation")
	}
	if tok, xerr := s.AccessToken(t.Context(), r.instance, req.RemoteID); xerr != nil || tok != b.token {
		t.Fatalf("delegated token: %+v %v", tok, xerr)
	}
	b.err = errx.NewWithIdentifier(errx.Forbidden, "mailbox_grant_inactive", "The grant is inactive.")
	if _, xerr := s.AccessToken(t.Context(), r.instance, req.RemoteID); xerr != b.err {
		t.Fatalf("grant refusal: %v", xerr)
	}
	b.err = nil
	b.token.Email = "another@example.test"
	if _, xerr := s.AccessToken(t.Context(), r.instance, req.RemoteID); xerr == nil {
		t.Fatal("token mailbox identity was not checked")
	}
	if xerr := s.Unenroll(t.Context(), r.instance, req.RemoteID); xerr != nil {
		t.Fatal(xerr)
	}
	calls := b.calls
	if _, xerr := s.AccessToken(t.Context(), r.instance, req.RemoteID); xerr == nil || b.calls != calls || emails.accounts[id] != acc {
		t.Fatal("revocation brokered a token or deleted the Cloud mailbox")
	}
}

func TestDelegatedAdoptionRequiresSameWorkspaceActiveGoogleMailbox(t *testing.T) {
	for _, refusal := range []string{"other_workspace", "inactive", "microsoft"} {
		t.Run(refusal, func(t *testing.T) {
			org, user, id := uuid.New(), uuid.New(), uuid.New()
			r := &managedOpsRepo{instance: &models.PoolLinkInstance{ID: uuid.New(), OrganizationID: org, CreatedBy: &user}}
			acc := &models.Email{ID: id, OrganizationID: &org, Status: "active", Provider: "gmail", AuthMethod: models.MailAuthDelegated}
			if refusal == "other_workspace" {
				other := uuid.New()
				acc.OrganizationID = &other
			}
			if refusal == "inactive" {
				acc.Status = "inactive"
			}
			if refusal == "microsoft" {
				acc.Provider = "outlook"
			}
			s := &service{repo: r, emails: &managedOpsEmails{accounts: map[uuid.UUID]*models.Email{id: acc}}}
			if _, xerr := s.Adopt(t.Context(), r.instance, models.PoolLinkAdoptRequest{Protocol: 1, RemoteID: uuid.New(), EmailAccountID: id}); xerr != ErrNotAdoptable || r.op != nil {
				t.Fatalf("adoption refusal: %v", xerr)
			}
		})
	}
}
