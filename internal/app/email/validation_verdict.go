package email

import (
	"fmt"
	"slices"
	"strings"

	"github.com/warmbly/warmbly/internal/config"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/pkg/mailcause"
	"github.com/warmbly/warmbly/internal/pkg/mailhost"
)

// Identifiers for a refused connect, one per kind of failure. The message
// names the leg, the host and what the server said; the code is what a client
// branches on.
const (
	ErrIDMailboxAuthRefused    = "mailbox_auth_refused"
	ErrIDMailboxUnreachable    = "mailbox_unreachable"
	ErrIDMailboxTLSFailed      = "mailbox_tls_failed"
	ErrIDMailboxServerDeclined = "mailbox_server_declined"
)

// reasonRank orders the reasons by how much they say. When the two legs fail
// differently, the code comes from the one that says the most: a refused
// password outranks a timeout on the other leg, because the timeout is not
// what the person has to fix first.
var reasonRank = map[string]int{
	models.MailProbeAuthRefused: 6,
	models.MailProbeTLS:         5,
	models.MailProbeUnreachable: 4,
	models.MailProbeCleartext:   3,
	models.MailProbeProtocol:    2,
	models.MailProbeTemporary:   1,
	models.MailProbeTimeout:     0,
}

// validationError turns a worker's verdict into the error the caller sees.
// creds carries the hosts and ports the verdict is about.
func validationError(v models.EmailValidationVerdict, creds *models.SmtpImap) *errx.Error {
	if creds == nil {
		creds = &models.SmtpImap{}
	}
	var legs []failedLeg
	if !v.SMTP.OK {
		legs = append(legs, failedLeg{"SMTP", creds.SMTP, v.SMTP})
	}
	if !v.IMAP.OK {
		legs = append(legs, failedLeg{"IMAP", creds.IMAP, v.IMAP})
	}
	if len(legs) == 0 {
		// A verdict that says "not ok" with both legs passing is malformed;
		// the legacy answer is the honest one.
		return errx.ErrEmailCredentials
	}

	lead := legs[0]
	for _, l := range legs[1:] {
		if reasonRank[l.res.Reason] > reasonRank[lead.res.Reason] {
			lead = l
		}
	}
	if lead.res.Reason == models.MailProbeTimeout {
		return timeoutError(v, creds, legs)
	}

	parts := make([]string, 0, len(legs))
	for _, l := range legs {
		parts = append(parts, legSentence(l.leg, l.svc, l.res))
	}
	msg := strings.Join(parts, " ") + " Nothing was saved."
	if hint := providerHint(lead); hint != "" {
		msg += " " + hint
	}
	for _, hint := range connectionHints(legs) {
		msg += " " + hint
	}

	id := ErrIDMailboxServerDeclined
	switch lead.res.Reason {
	case models.MailProbeAuthRefused:
		id = ErrIDMailboxAuthRefused
	case models.MailProbeUnreachable:
		id = ErrIDMailboxUnreachable
	case models.MailProbeTLS:
		id = ErrIDMailboxTLSFailed
	}
	e := errx.NewWithIdentifier(errx.BadRequest, id, msg)
	if id == ErrIDMailboxTLSFailed && slices.ContainsFunc(legs, untrustedCertificate) {
		e.Reason = ReasonCertificateUntrusted
	}
	return withCause(e, v, creds, lead)
}

// withCause attaches the grouping key an import reads; a single connect ignores it.
func withCause(e *errx.Error, v models.EmailValidationVerdict, creds *models.SmtpImap, lead failedLeg) *errx.Error {
	in := mailcause.Input{SMTP: causeLeg("SMTP", creds.SMTP, v.SMTP), IMAP: causeLeg("IMAP", creds.IMAP, v.IMAP)}
	if creds.SMTP != nil {
		in.MailHost = string(mailhost.FromServer(creds.SMTP.Host))
		in.Password = creds.SMTP.Password
	}
	e.Cause = mailcause.Classify(in).Key
	e.Detail = mailcause.Scrub(lead.res.Detail)
	return e
}

func causeLeg(name string, svc *models.Service, res models.EmailValidationLeg) mailcause.Leg {
	leg := mailcause.Leg{Name: name, OK: res.OK, Reason: res.Reason, Detail: res.Detail}
	if svc != nil {
		leg.Host, leg.Port = models.NormalizeMailHost(svc.Host), svc.Port
	}
	return leg
}

// timeoutError is the verdict where the leg that says the most stayed silent.
// It names that leg and says whether the other one got through, because a
// port that hangs while the other passes is a network in the way, not a
// password: many hosts block outbound 465 and leave 587 open.
func timeoutError(v models.EmailValidationVerdict, creds *models.SmtpImap, failed []failedLeg) *errx.Error {
	parts := make([]string, 0, 2)
	for _, l := range []failedLeg{{"SMTP", creds.SMTP, v.SMTP}, {"IMAP", creds.IMAP, v.IMAP}} {
		if l.res.OK {
			parts = append(parts, legWhere(l.leg, l.svc, l.res)+" signed in.")
			continue
		}
		parts = append(parts, legSentence(l.leg, l.svc, l.res))
	}
	msg := strings.Join(parts, " ") + " Nothing was saved. Check the host and port, then try again."
	for _, l := range failed {
		if l.leg == "SMTP" && l.res.Reason == models.MailProbeTimeout && l.svc != nil && l.svc.Port == 465 {
			msg += " Port 465 did not answer and the worker could not use 587 with STARTTLS on the same server either. Some networks block outbound mail ports; check that the server is reachable from outside and which port it listens on."
			break
		}
	}
	lead := failed[0]
	return withCause(errx.NewWithIdentifier(errx.BadRequest, errx.ErrEmailValidation.Identifier, msg), v, creds, lead)
}

// legWhere names a leg by its server when the caller gave one, and by the port
// the worker ended up on when that differs from the one asked for.
func legWhere(leg string, svc *models.Service, res models.EmailValidationLeg) string {
	if svc == nil {
		return leg + " server"
	}
	host := models.NormalizeMailHost(svc.Host)
	if res.Port != 0 && res.Port != svc.Port {
		return fmt.Sprintf("%s (%s, after %d did not answer)", leg, models.MailDialAddress(host, res.Port), svc.Port)
	}
	return fmt.Sprintf("%s (%s)", leg, models.MailDialAddress(host, svc.Port))
}

// legSentence is one leg's failure in the server's own words.
func legSentence(leg string, svc *models.Service, res models.EmailValidationLeg) string {
	where := legWhere(leg, svc, res)
	detail := ""
	if res.Detail != "" {
		detail = ": " + res.Detail
	}
	switch res.Reason {
	case models.MailProbeAuthRefused:
		return fmt.Sprintf("%s refused the sign-in%s.", where, detail)
	case models.MailProbeUnreachable:
		return fmt.Sprintf("%s could not be reached from the worker%s.", where, detail)
	case models.MailProbeTLS:
		return fmt.Sprintf("%s did not complete a secure connection%s.%s", where, detail, presented(res.Certificate))
	case models.MailProbeTemporary:
		return fmt.Sprintf("%s declined the sign-in for now and asks for a retry%s.", where, detail)
	case models.MailProbeCleartext:
		return fmt.Sprintf("%s cannot be used without encryption%s.", where, detail)
	case models.MailProbeTimeout:
		return fmt.Sprintf("%s did not answer in time.", where)
	default:
		return fmt.Sprintf("%s did not accept the sign-in%s.", where, detail)
	}
}

// presented names the certificate a server showed, so its owner can compare it before trusting it.
func presented(cert *models.MailCertificate) string {
	if cert == nil || cert.Fingerprint == "" {
		return ""
	}
	s := " It presented a certificate"
	if cert.Subject != "" {
		s += " for " + cert.Subject
	}
	if cert.SelfSigned {
		s += " (self-signed)"
	} else if cert.Issuer != "" {
		s += " issued by " + cert.Issuer
	}
	return s + " with SHA-256 key fingerprint " + cert.Fingerprint + "."
}

// ReasonCertificateUntrusted is the response reason on a mailbox_tls_failed the owner can resolve by trusting the server's own certificate.
const ReasonCertificateUntrusted = "certificate_untrusted"

// TrustCertificateHint follows a failure on a certificate no authority vouches for.
const TrustCertificateHint = "If this is your own server with a self-signed or company certificate, compare that fingerprint with the one its owner gives you, then test again trusting the server's own certificate (tls_unverified in the API)."

// untrustedCertificate reports a leg that failed on a certificate no authority vouches for and that is not already trusted on its own.
func untrustedCertificate(l failedLeg) bool {
	return l.res.Reason == models.MailProbeTLS && l.res.Certificate != nil && !l.res.Certificate.Trusted &&
		l.res.Certificate.Fingerprint != "" && (l.svc == nil || !l.svc.TLSUnverified)
}

// connectionHints says what to do about failures the server's own words do not explain.
func connectionHints(legs []failedLeg) []string {
	var private, untrusted bool
	for _, l := range legs {
		switch {
		case l.res.Reason == models.MailProbeUnreachable && l.res.Detail == models.MailProbeDetailPrivateHost:
			private = true
		case untrustedCertificate(l):
			untrusted = true
		}
	}
	var hints []string
	if private {
		hint := "Use the host name your provider gives for connecting from outside your network."
		if config.SelfHosted() {
			hint += " To connect mailboxes on a private network, the instance's operator can set MAIL_ALLOW_PRIVATE_HOSTS=true."
		}
		hints = append(hints, hint)
	}
	if untrusted {
		hints = append(hints, TrustCertificateHint)
	}
	return hints
}

// failedLeg is one side of a verdict that did not pass, with the service it
// was about.
type failedLeg struct {
	leg string
	svc *models.Service
	res models.EmailValidationLeg
}

// providerHint adds the one thing worth saying about a well-known provider.
func providerHint(lead failedLeg) string {
	if lead.res.Reason != models.MailProbeAuthRefused || lead.svc == nil {
		return ""
	}
	if models.GoogleMailHost(lead.svc.Host) {
		return "Google itself refused this address and password. Use a 16-letter app password from myaccount.google.com/apppasswords, created while signed in to the Google account that owns this address, and enter the account's own sign-in address rather than an alias or a group. A deleted app password stops working at once, and on Google Workspace the administrator can block app passwords or IMAP."
	}
	return ""
}
