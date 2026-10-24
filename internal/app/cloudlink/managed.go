package cloudlink

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// Cloud-managed mailboxes: the grant lives on Warmbly Cloud; this instance sends with brokered access tokens.

var (
	ErrNotManaged   = errx.NewWithIdentifier(errx.NotFound, "cloud_link_not_managed", "This mailbox is not managed by Warmbly Cloud.")
	ErrOAuthSession = errx.NewWithIdentifier(errx.NotFound, "cloud_link_oauth_session", "That sign-in session is unknown or has expired. Start again.")
)

// OAuthReturnPath is the dashboard route the cloud sends the popup back to.
const OAuthReturnPath = "/cloud-oauth/done"

// tokenCacheMax bounds how long a brokered token is reused before the cloud is asked again.
const tokenCacheMax = 10 * time.Minute

const workspaceMailboxPath = "/instance/workspace-mailboxes?include_smtp_imap=true"

type cachedToken struct {
	token   *models.PoolLinkAccessToken
	expires time.Time
}

func (s *service) StartOAuth(ctx context.Context, orgID, userID uuid.UUID, provider models.InboxProvider) (*models.CloudLinkOAuthStart, *errx.Error) {
	if !reconciliationLocked(ctx) {
		var result *models.CloudLinkOAuthStart
		xerr := s.reconcileLocked(ctx, func(ctx context.Context) *errx.Error {
			var xerr *errx.Error
			result, xerr = s.StartOAuth(ctx, orgID, userID, provider)
			return xerr
		})
		return result, xerr
	}
	return s.startManagedOAuth(ctx, orgID, userID, provider)
}

func (s *service) FinishOAuth(ctx context.Context, orgID, userID uuid.UUID, session string) (*models.Email, *errx.Error) {
	if !reconciliationLocked(ctx) {
		var result *models.Email
		xerr := s.reconcileLocked(ctx, func(ctx context.Context) *errx.Error {
			var xerr *errx.Error
			result, xerr = s.FinishOAuth(ctx, orgID, userID, session)
			return xerr
		})
		return result, xerr
	}
	return s.finishManagedOAuth(ctx, orgID, userID, session)
}

func (s *service) ListWorkspaceMailboxes(ctx context.Context, orgID uuid.UUID) ([]models.PoolLinkWorkspaceMailbox, *errx.Error) {
	l, xerr := s.link(ctx, orgID)
	if xerr != nil {
		return nil, xerr
	}
	var list []models.PoolLinkWorkspaceMailbox
	if xerr := s.clientFor(l).do(ctx, http.MethodGet, workspaceMailboxPath, nil, &list); xerr != nil {
		return nil, xerr
	}
	if list == nil {
		list = []models.PoolLinkWorkspaceMailbox{}
	}
	var addresses []string
	for _, mailbox := range list {
		if mailbox.Provider == string(models.InboxProviderSMTPIMAP) {
			addresses = append(addresses, mailbox.Email)
		}
	}
	if len(addresses) == 0 {
		return list, nil
	}
	refs, xerr := s.emails.FindManyInOrganization(ctx, orgID, addresses)
	if xerr != nil {
		return nil, xerr
	}
	enrollments, err := s.repo.ListForOrg(ctx, orgID, nil)
	if err != nil {
		return nil, errx.InternalError()
	}
	byAccount := make(map[uuid.UUID]models.CloudLinkMailbox, len(enrollments))
	for _, enrollment := range enrollments {
		byAccount[enrollment.EmailAccountID] = enrollment
	}
	filtered := make([]models.PoolLinkWorkspaceMailbox, 0, len(list))
	for _, mailbox := range list {
		if mailbox.Provider == string(models.InboxProviderSMTPIMAP) {
			ref, found := refs[strings.ToLower(mailbox.Email)]
			if !found || ref.Status != "active" || ref.Provider != mailbox.Provider || ref.Managed {
				continue
			}
			if m, found := byAccount[ref.ID]; found && (m.InstanceID != l.InstanceID || m.Managed || m.EnrollmentState != "pending_enroll") {
				continue
			}
		}
		filtered = append(filtered, mailbox)
	}
	return filtered, nil
}

func (s *service) connectExistingSMTPIMAP(ctx context.Context, orgID uuid.UUID, l *models.CloudLink, cloud models.PoolLinkWorkspaceMailbox) (*models.Email, *errx.Error) {
	ref, xerr := s.emails.FindInOrganization(ctx, orgID, cloud.Email)
	if xerr != nil {
		return nil, xerr
	}
	if ref == nil || ref.Provider != string(models.InboxProviderSMTPIMAP) || ref.Status != "active" || ref.Managed {
		return nil, errx.ErrEmailCredentials
	}
	acc, xerr := s.ownedAccount(ctx, orgID, ref.ID)
	if xerr != nil {
		return nil, xerr
	}
	if !strings.EqualFold(acc.Email, cloud.Email) {
		return nil, errx.ErrNotFound
	}
	m, err := s.repo.GetByAccount(ctx, acc.ID)
	if err != nil {
		return nil, errx.InternalError()
	}
	if m != nil && (m.InstanceID != l.InstanceID || m.Managed || m.EnrollmentState != "pending_enroll") {
		return nil, errx.ErrEmailOnboardAlreadyExists
	}
	if m != nil && m.SMTPCloudAccountID != nil && *m.SMTPCloudAccountID != cloud.ID {
		return nil, errx.ErrEmailOnboardAlreadyExists
	}
	var info models.PoolLinkInstanceInfo
	if xerr := s.clientFor(l).do(ctx, http.MethodGet, "/instance", nil, &info); xerr != nil {
		return nil, xerr
	}
	if info.Instance.ID != l.InstanceID || info.SMTPAssociationProtocol != models.SMTPAssociationProtocol {
		return nil, errx.NewWithIdentifier(errx.Conflict, "cloud_link_smtp_association_protocol", "Update Warmbly Cloud before connecting an existing app-password mailbox. Keep both mailbox records; no passwords or history need replacing.")
	}
	r, ok := s.repo.(repository.CloudSMTPAssociationRepository)
	if !ok {
		return nil, errx.InternalError()
	}
	if err := r.BeginSMTPAssociation(ctx, acc.ID, l.InstanceID, cloud.ID); err != nil {
		return nil, errx.InternalError()
	}
	m, err = s.repo.GetByAccount(ctx, acc.ID)
	if err != nil || m == nil || m.SMTPCloudAccountID == nil || *m.SMTPCloudAccountID != cloud.ID || m.InstanceID != l.InstanceID || m.EnrollmentState != "pending_enroll" {
		return nil, errx.InternalError()
	}
	s.syncLocalPool(ctx, acc.ID)
	var state models.PoolLinkMailboxState
	req := models.PoolLinkSMTPAssociationRequest{RemoteID: m.RemoteID, EmailAccountID: cloud.ID, Email: acc.Email}
	if xerr := s.clientFor(l).do(ctx, http.MethodPost, "/instance/mailboxes/associate-smtp", req, &state); xerr != nil {
		if xerr.Identifier == "pool_link_mailbox_conflict" {
			return nil, errx.NewWithIdentifier(errx.Conflict, "cloud_link_smtp_association", "This existing Cloud mailbox could not be linked. Check that it belongs to the linked Cloud workspace and is not used by an active Cloud campaign or another instance. Keep the Cloud mailbox to preserve its history.")
		}
		return nil, xerr
	}
	if state.RemoteID != m.RemoteID || state.EmailAccountID != cloud.ID || state.Provider != acc.Provider || !state.Managed || state.Status != "active" || !strings.EqualFold(state.Email, acc.Email) {
		return nil, ErrOAuthSession
	}
	if _, err := s.repo.Enroll(ctx, acc.ID, state.RemoteID, l.InstanceID, false); err != nil {
		return nil, errx.InternalError()
	}
	s.recordStanding(ctx, acc.ID, state.Health, true)
	s.mirrorReported(ctx, acc.ID, &state)
	s.syncLocalPool(ctx, acc.ID)
	return s.emails.GetByID(ctx, acc.ID)
}

func (s *service) recoverExistingSMTPIMAP(ctx context.Context, orgID uuid.UUID, l *models.CloudLink, cloudAccountID uuid.UUID) (*models.Email, *errx.Error) {
	var states []models.PoolLinkMailboxState
	if xerr := s.clientFor(l).do(ctx, http.MethodGet, "/instance/mailboxes", nil, &states); xerr != nil {
		return nil, xerr
	}
	enrollments, err := s.repo.ListForOrg(ctx, orgID, nil)
	if err != nil {
		return nil, errx.InternalError()
	}
	byRemote := map[uuid.UUID]models.CloudLinkMailbox{}
	for _, m := range enrollments {
		if m.InstanceID == l.InstanceID {
			byRemote[m.RemoteID] = m
		}
	}
	for _, state := range states {
		if state.EmailAccountID != cloudAccountID || state.Provider != string(models.InboxProviderSMTPIMAP) || state.Status != "active" {
			continue
		}
		m, found := byRemote[state.RemoteID]
		if !found {
			return nil, ErrOAuthSession
		}
		acc, xerr := s.ownedAccount(ctx, orgID, m.EmailAccountID)
		if xerr != nil {
			return nil, xerr
		}
		if m.InstanceID != l.InstanceID || m.RemoteID != state.RemoteID || m.Managed || m.EnrollmentState == "pending_remove" ||
			(m.SMTPCloudAccountID != nil && *m.SMTPCloudAccountID != cloudAccountID) ||
			acc.Provider != state.Provider || acc.Status != "active" || !strings.EqualFold(acc.Email, state.Email) {
			return nil, ErrOAuthSession
		}
		if m.EnrollmentState == "pending_enroll" {
			if _, err := s.repo.Enroll(ctx, acc.ID, state.RemoteID, l.InstanceID, false); err != nil {
				return nil, errx.InternalError()
			}
			s.recordStanding(ctx, acc.ID, state.Health, true)
			s.mirrorReported(ctx, acc.ID, &state)
			s.syncLocalPool(ctx, acc.ID)
		}
		return acc, nil
	}
	return nil, ErrOAuthSession
}

func (s *service) Adopt(ctx context.Context, orgID, userID, cloudAccountID uuid.UUID) (*models.Email, *errx.Error) {
	if !reconciliationLocked(ctx) {
		var result *models.Email
		xerr := s.reconcileLocked(ctx, func(ctx context.Context) *errx.Error {
			var xerr *errx.Error
			result, xerr = s.Adopt(ctx, orgID, userID, cloudAccountID)
			return xerr
		})
		return result, xerr
	}
	return s.adoptManagedMailbox(ctx, orgID, userID, cloudAccountID)
}

// AccessToken is the worker's credential for a managed mailbox, cached briefly.
func (s *service) AccessToken(ctx context.Context, accountID uuid.UUID) (*models.PoolLinkAccessToken, *errx.Error) {
	if r, ok := s.repo.(repository.CloudManagedConsentRepository); ok {
		allowed, err := r.CanBrokerManagedToken(ctx, accountID)
		if err != nil {
			return nil, errx.InternalError()
		}
		if !allowed {
			return nil, ErrMailboxInactive
		}
	}
	s.mu.Lock()
	if c, ok := s.tokens[accountID]; ok && time.Now().Before(c.expires) {
		s.mu.Unlock()
		return c.token, nil
	}
	s.mu.Unlock()

	m, err := s.repo.GetByAccount(ctx, accountID)
	if err != nil {
		return nil, errx.InternalError()
	}
	if m == nil || !m.Managed {
		return nil, ErrNotManaged
	}
	l, xerr := s.mailboxLink(ctx, m)
	if xerr != nil {
		return nil, xerr
	}
	var tok models.PoolLinkAccessToken
	if xerr := s.clientFor(l).do(ctx, http.MethodGet, "/instance/mailboxes/"+m.RemoteID.String()+"/token", nil, &tok); xerr != nil {
		return nil, xerr
	}
	// Capped so a cloud-side revocation bites within minutes.
	until := tok.ExpiresAt.Add(-2 * time.Minute)
	if cap := time.Now().Add(tokenCacheMax); until.After(cap) {
		until = cap
	}
	s.mu.Lock()
	s.tokens[accountID] = cachedToken{token: &tok, expires: until}
	s.mu.Unlock()
	return &tok, nil
}

func (s *service) forgetToken(accountID uuid.UUID) {
	s.mu.Lock()
	delete(s.tokens, accountID)
	s.mu.Unlock()
}

// removeManaged deletes the local mirror; the cloud keeps the mailbox in the workspace.
func (s *service) removeManaged(ctx context.Context, orgID string, m *models.CloudLinkMailbox) *errx.Error {
	if err := s.repo.BeginRemoval(ctx, m.EmailAccountID); err != nil {
		return errx.InternalError()
	}
	l, err := s.repo.GetByInstance(ctx, m.InstanceID)
	if err != nil {
		return errx.InternalError()
	}
	if l == nil {
		return ErrNotConnected
	}
	if r, ok := s.repo.(repository.CloudManagedConsentRepository); ok {
		if err := r.RevokeManagedConsents(ctx, l.InstanceID, &m.EmailAccountID); err != nil {
			return errx.InternalError()
		}
	}
	if xerr := s.clientFor(l).do(ctx, http.MethodDelete, "/instance/mailboxes/"+m.RemoteID.String(), nil, nil); xerr != nil && xerr.Identifier != "pool_link_mailbox_not_found" && !linkAlreadyGone(xerr) {
		return xerr
	}
	s.forgetToken(m.EmailAccountID)
	if s.emailSvc != nil {
		if xerr := s.emailSvc.Delete(ctx, orgID, m.EmailAccountID.String()); xerr != nil && xerr != errx.ErrNotFound {
			return xerr
		}
	} else {
		return errx.InternalError()
	}
	if err := s.repo.Unenroll(ctx, m.EmailAccountID); err != nil {
		return errx.InternalError()
	}
	return nil
}

// VerifyWarmupToken asks the cloud whether warmup mail in an enrolled mailbox is its own.
func (s *service) VerifyWarmupToken(ctx context.Context, accountID uuid.UUID, token string) (bool, error) {
	m, err := s.repo.GetByAccount(ctx, accountID)
	if err != nil || m == nil {
		return false, err
	}
	l, xerr := s.mailboxLink(ctx, m)
	if xerr != nil {
		return false, xerr
	}
	var out struct {
		Valid bool `json:"valid"`
	}
	if xerr := s.clientFor(l).do(ctx, http.MethodGet, "/instance/mailboxes/"+m.RemoteID.String()+"/warmup-tokens/"+url.PathEscape(token), nil, &out); xerr != nil {
		return false, xerr
	}
	return out.Valid, nil
}

// IsCloudWarmupDelivery asks the cloud whether a message that arrived without a
// verify header is its own warmup mail. Every send from a Microsoft mailbox
// loses the header in transit, so without this the cloud's warmup would be
// filed as ordinary mail in the owner's inbox.
func (s *service) IsCloudWarmupDelivery(ctx context.Context, accountID uuid.UUID, sender, messageID, subject string) (bool, error) {
	if messageID == "" && (sender == "" || subject == "") {
		return false, nil
	}
	m, err := s.repo.GetByAccount(ctx, accountID)
	if err != nil || m == nil {
		return false, err
	}
	l, xerr := s.mailboxLink(ctx, m)
	if xerr != nil {
		return false, xerr
	}
	var out struct {
		Valid bool `json:"valid"`
	}
	q := models.PoolLinkWarmupDeliveryQuery{Sender: sender, MessageID: messageID, Subject: subject}
	if xerr := s.clientFor(l).do(ctx, http.MethodPost, "/instance/mailboxes/"+m.RemoteID.String()+"/warmup-deliveries", q, &out); xerr != nil {
		return false, xerr
	}
	return out.Valid, nil
}

// IsCloudWarmupThreadReply asks the cloud whether a tokenless message answers
// a turn of one of its warmup conversations. The cloud records a yes itself,
// so the turn answering this one is recognised on the next ask.
func (s *service) IsCloudWarmupThreadReply(ctx context.Context, accountID uuid.UUID, messageID string, inReplyTo []string) (bool, error) {
	if len(inReplyTo) == 0 {
		return false, nil
	}
	m, err := s.repo.GetByAccount(ctx, accountID)
	if err != nil || m == nil {
		return false, err
	}
	l, xerr := s.mailboxLink(ctx, m)
	if xerr != nil {
		return false, xerr
	}
	var out struct {
		Valid bool `json:"valid"`
	}
	q := models.PoolLinkWarmupDeliveryQuery{MessageID: messageID, InReplyTo: inReplyTo}
	if xerr := s.clientFor(l).do(ctx, http.MethodPost, "/instance/mailboxes/"+m.RemoteID.String()+"/warmup-deliveries", q, &out); xerr != nil {
		return false, xerr
	}
	return out.Valid, nil
}
