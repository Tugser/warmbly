package email

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// smtpIMAPMailbox loads an SMTP/IMAP mailbox of the organization with its stored credentials.
func (s *emailService) smtpIMAPMailbox(ctx context.Context, orgID *uuid.UUID, accountID uuid.UUID) (*models.Email, *repository.SMTPCredentials, *errx.Error) {
	if orgID == nil {
		return nil, nil, errx.ErrNoOrganization
	}
	account, xerr := s.emailRepository.GetByID(ctx, accountID)
	if xerr != nil {
		return nil, nil, xerr
	}
	if account == nil || account.OrganizationID == nil || *account.OrganizationID != *orgID {
		return nil, nil, errx.ErrNotFound
	}
	if models.InboxProvider(account.Provider) != models.InboxProviderSMTPIMAP {
		return nil, nil, errx.ErrEmailReauthOAuthOnly
	}
	stored, xerr := s.emailRepository.GetSMTPCredentials(ctx, accountID)
	if xerr != nil {
		return nil, nil, xerr
	}
	return account, stored, nil
}

func storedServices(c *repository.SMTPCredentials) *models.SmtpImap {
	return &models.SmtpImap{
		SMTP: &models.Service{Host: c.SMTPHost, Port: c.SMTPPort, Username: c.SMTPUser, Password: c.SMTPPassword, Security: c.SMTPSecurity, TLSUnverified: c.SMTPTLSUnverified, TLSPin: c.SMTPTLSPin},
		IMAP: &models.Service{Host: c.IMAPHost, Port: c.IMAPPort, Username: c.IMAPUser, Password: c.IMAPPassword, Security: c.IMAPSecurity, TLSUnverified: c.IMAPTLSUnverified, TLSPin: c.IMAPTLSPin},
	}
}

func serverCertificate(svc *models.Service) models.MailboxServerCertificate {
	out := models.MailboxServerCertificate{
		Host: models.NormalizeMailHost(svc.Host), Port: svc.Port,
		TLSUnverified: svc.TLSUnverified, Observed: svc.Observed,
	}
	if svc.TLSUnverified {
		out.TLSPin = models.NormalizeTLSPin(svc.TLSPin)
	}
	return out
}

func certificatesOf(creds *models.SmtpImap) *models.MailboxCertificates {
	return &models.MailboxCertificates{SMTP: serverCertificate(creds.SMTP), IMAP: serverCertificate(creds.IMAP)}
}

func (s *emailService) MailboxCertificates(ctx context.Context, orgID *uuid.UUID, accountID uuid.UUID) (*models.MailboxCertificates, *errx.Error) {
	_, stored, xerr := s.smtpIMAPMailbox(ctx, orgID, accountID)
	if xerr != nil {
		return nil, xerr
	}
	return certificatesOf(storedServices(stored)), nil
}

func (s *emailService) CheckMailboxCertificates(ctx context.Context, orgID *uuid.UUID, accountID uuid.UUID) (*models.MailboxCertificates, *errx.Error) {
	ctx, cancel := detach(ctx, connectBudget)
	defer cancel()
	account, stored, xerr := s.smtpIMAPMailbox(ctx, orgID, accountID)
	if xerr != nil {
		return nil, xerr
	}
	// The pins are left out so the test reports what each server presents
	// now; the stored policy is what the answer is compared against.
	probe := storedServices(stored)
	probe.SMTP.TLSPin, probe.IMAP.TLSPin = "", ""
	if xerr := s.checkCredentials(ctx, *orgID, account.WorkerID, probe); xerr != nil {
		return nil, xerr
	}
	out := certificatesOf(storedServices(stored))
	out.SMTP.Observed, out.IMAP.Observed = probe.SMTP.Observed, probe.IMAP.Observed
	return out, nil
}

func (s *emailService) ConfirmMailboxCertificates(ctx context.Context, orgID *uuid.UUID, accountID uuid.UUID, req models.ConfirmMailboxCertificates) (*models.MailboxCertificates, *models.Email, *errx.Error) {
	ctx, cancel := detach(ctx, connectBudget)
	defer cancel()
	account, stored, xerr := s.smtpIMAPMailbox(ctx, orgID, accountID)
	if xerr != nil {
		return nil, nil, xerr
	}
	creds := storedServices(stored)
	if !creds.SMTP.TLSUnverified && !creds.IMAP.TLSUnverified {
		return nil, nil, errx.ErrEmailCertificateNotPinned
	}
	for _, leg := range []struct {
		svc *models.Service
		pin string
	}{{creds.SMTP, req.SMTPPin}, {creds.IMAP, req.IMAPPin}} {
		if !leg.svc.TLSUnverified {
			continue
		}
		pin := models.NormalizeTLSPin(leg.pin)
		if pin == "" {
			return nil, nil, errx.ErrEmailCertificatePin
		}
		leg.svc.TLSPin = pin
	}
	// The worker refuses any certificate other than the confirmed pin.
	if xerr := s.checkCredentials(ctx, *orgID, account.WorkerID, creds); xerr != nil {
		if xerr.Identifier == ErrIDMailboxTLSFailed && strings.Contains(xerr.Message, models.MailProbeDetailPinMismatch) {
			return nil, nil, errx.ErrEmailCertificatePinMismatch
		}
		return nil, nil, xerr
	}
	if err := s.emailRepository.ReplaceSMTPIMAPCredentials(ctx, accountID, creds); err != nil {
		return nil, nil, errx.InternalError()
	}
	// The mailbox keeps its status; an active one is reloaded with the new pin.
	s.resolveCredentialErrors(ctx, accountID)
	if err := s.LoadAccountOntoWorker(ctx, accountID); err != nil {
		log.Warn().Err(err).Str("account_id", accountID.String()).Msg("certificate confirmed; worker reload failed and the reconciler will retry")
	}
	return certificatesOf(creds), account, nil
}
