package email

import (
	"strings"
	"testing"

	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

func gmailCreds() *models.SmtpImap {
	return &models.SmtpImap{
		SMTP: &models.Service{Host: "smtp.gmail.com", Port: 465, Username: "a@gmail.com"},
		IMAP: &models.Service{Host: "imap.gmail.com", Port: 993, Username: "a@gmail.com"},
	}
}

// TestValidationError_AuthRefused: a refused password names both servers, quotes
// what they said, and tells a Gmail user what an app password is.
func TestValidationError_AuthRefused(t *testing.T) {
	xerr := validationError(models.EmailValidationVerdict{
		SMTP: models.EmailValidationLeg{Reason: models.MailProbeAuthRefused, Detail: "535 5.7.8 Username and Password not accepted"},
		IMAP: models.EmailValidationLeg{Reason: models.MailProbeAuthRefused, Detail: "imap: NO [AUTHENTICATIONFAILED] Invalid credentials (Failure)"},
	}, gmailCreds())
	if xerr.Identifier != ErrIDMailboxAuthRefused || xerr.Code != errx.BadRequest {
		t.Fatalf("got %s/%v", xerr.Identifier, xerr.Code)
	}
	for _, want := range []string{"smtp.gmail.com:465", "5.7.8", "imap.gmail.com:993", "AUTHENTICATIONFAILED", "app password", "Nothing was saved"} {
		if !strings.Contains(xerr.Message, want) {
			t.Fatalf("message %q lacks %q", xerr.Message, want)
		}
	}
}

// TestValidationError_Ranking: the code comes from the leg that says the most,
// and a timeout alone is still the timeout error the docs describe.
func TestValidationError_Ranking(t *testing.T) {
	ok := models.EmailValidationLeg{OK: true}
	cases := []struct {
		name string
		smtp models.EmailValidationLeg
		imap models.EmailValidationLeg
		want string
	}{
		{"timeout alone", models.EmailValidationLeg{Reason: models.MailProbeTimeout}, ok, errx.ErrEmailValidation.Identifier},
		{"unreachable beats timeout", models.EmailValidationLeg{Reason: models.MailProbeUnreachable, Detail: "dial tcp: connection refused"}, models.EmailValidationLeg{Reason: models.MailProbeTimeout}, ErrIDMailboxUnreachable},
		{"refusal beats unreachable", models.EmailValidationLeg{Reason: models.MailProbeUnreachable}, models.EmailValidationLeg{Reason: models.MailProbeAuthRefused}, ErrIDMailboxAuthRefused},
		{"tls", ok, models.EmailValidationLeg{Reason: models.MailProbeTLS, Detail: "tls: failed to verify certificate"}, ErrIDMailboxTLSFailed},
		{"temporary", models.EmailValidationLeg{Reason: models.MailProbeTemporary}, ok, ErrIDMailboxServerDeclined},
		{"protocol", ok, models.EmailValidationLeg{Reason: models.MailProbeProtocol}, ErrIDMailboxServerDeclined},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			xerr := validationError(models.EmailValidationVerdict{SMTP: tc.smtp, IMAP: tc.imap}, gmailCreds())
			if xerr.Identifier != tc.want {
				t.Fatalf("identifier = %q, want %q (%s)", xerr.Identifier, tc.want, xerr.Message)
			}
		})
	}
	// A verdict that fails with no failing leg is malformed and falls back.
	if xerr := validationError(models.EmailValidationVerdict{SMTP: ok, IMAP: ok}, gmailCreds()); xerr != errx.ErrEmailCredentials {
		t.Fatalf("got %v, want the legacy error", xerr)
	}
	// No hint for a provider we know nothing about.
	other := &models.SmtpImap{SMTP: &models.Service{Host: "mail.example.com", Port: 587}, IMAP: &models.Service{Host: "mail.example.com", Port: 993}}
	xerr := validationError(models.EmailValidationVerdict{SMTP: models.EmailValidationLeg{Reason: models.MailProbeAuthRefused}, IMAP: ok}, other)
	if strings.Contains(xerr.Message, "app password") {
		t.Fatalf("Gmail hint on a non-Google host: %s", xerr.Message)
	}
}

// TestValidationError_TimeoutNamesLeg: a silent leg is named, the leg that
// got through is said to have, and a hanging 465 points at 587.
func TestValidationError_TimeoutNamesLeg(t *testing.T) {
	ok := models.EmailValidationLeg{OK: true}
	xerr := validationError(models.EmailValidationVerdict{
		SMTP: models.EmailValidationLeg{Reason: models.MailProbeTimeout},
		IMAP: ok,
	}, gmailCreds())
	if xerr.Identifier != errx.ErrEmailValidation.Identifier || xerr.Code != errx.BadRequest {
		t.Fatalf("got %s/%v", xerr.Identifier, xerr.Code)
	}
	for _, want := range []string{"SMTP (smtp.gmail.com:465) did not answer in time", "IMAP (imap.gmail.com:993) signed in", "Nothing was saved", "587"} {
		if !strings.Contains(xerr.Message, want) {
			t.Fatalf("message %q lacks %q", xerr.Message, want)
		}
	}
	// Both silent: nothing signed in, and 587 is not offered for an IMAP port.
	xerr = validationError(models.EmailValidationVerdict{
		SMTP: models.EmailValidationLeg{Reason: models.MailProbeTimeout},
		IMAP: models.EmailValidationLeg{Reason: models.MailProbeTimeout},
	}, &models.SmtpImap{SMTP: &models.Service{Host: "mail.example.com", Port: 587}, IMAP: &models.Service{Host: "mail.example.com", Port: 993}})
	if strings.Contains(xerr.Message, "signed in") || strings.Contains(xerr.Message, "block outbound") {
		t.Fatalf("unexpected message: %s", xerr.Message)
	}
	if !strings.Contains(xerr.Message, "IMAP (mail.example.com:993) did not answer in time") {
		t.Fatalf("message %q does not name the IMAP leg", xerr.Message)
	}
}

// TestNormalizeMailPasswords: a Google app password loses every space, any
// other password only what is around it, and a nil leg is left alone.
func TestNormalizeMailPasswords(t *testing.T) {
	smtp := &models.Service{Host: "smtp.gmail.com", Password: " abcd efgh\tijkl mnop\n"}
	imap := &models.Service{Host: "IMAP.GMAIL.COM.", Password: "abcd efgh ijkl mnop"}
	other := &models.Service{Host: "mail.example.com", Password: "  pa ss word \n"}
	normalizeMailPasswords(smtp, imap, other, nil)
	if smtp.Password != "abcdefghijklmnop" || imap.Password != "abcdefghijklmnop" {
		t.Fatalf("gmail: %q / %q", smtp.Password, imap.Password)
	}
	if other.Password != "pa ss word" {
		t.Fatalf("other: %q", other.Password)
	}
}

// TestAdoptProbedPort: a pass reached on 587 after 465 stayed silent is stored
// with 587 and STARTTLS; a pass on the port asked for changes nothing.
func TestAdoptProbedPort(t *testing.T) {
	svc := &models.Service{Host: "smtp.gmail.com", Port: 465, Security: models.MailSecurityTLS}
	adoptProbedPort(svc, models.EmailValidationLeg{OK: true})
	if svc.Port != 465 || svc.Security != models.MailSecurityTLS {
		t.Fatalf("own port rewritten: %+v", svc)
	}
	adoptProbedPort(svc, models.EmailValidationLeg{OK: true, Port: 587, Security: models.MailSecurityStartTLS})
	if svc.Port != 587 || svc.Security != models.MailSecurityStartTLS {
		t.Fatalf("probed port not adopted: %+v", svc)
	}
	adoptProbedPort(nil, models.EmailValidationLeg{OK: true, Port: 587})
}

// TestValidationError_NamesTheFallbackPort: a refusal that came back on 587
// says so, because the person is looking at a form that says 465.
func TestValidationError_NamesTheFallbackPort(t *testing.T) {
	creds := &models.SmtpImap{
		SMTP: &models.Service{Host: "smtp.gmail.com", Port: 465, Security: models.MailSecurityTLS},
		IMAP: &models.Service{Host: "imap.gmail.com", Port: 993},
	}
	v := models.EmailValidationVerdict{
		SMTP: models.EmailValidationLeg{Reason: models.MailProbeAuthRefused, Detail: "535 5.7.8 Username and Password not accepted", Port: 587, Security: models.MailSecurityStartTLS},
		IMAP: models.EmailValidationLeg{OK: true},
	}
	xerr := validationError(v, creds)
	if !strings.Contains(xerr.Message, "SMTP (smtp.gmail.com:587, after 465 did not answer) refused the sign-in") {
		t.Fatalf("got %q", xerr.Message)
	}
}

// TestValidationError_PrivateHost: the operator setting is named only where an operator can change it.
func TestValidationError_PrivateHost(t *testing.T) {
	leg := models.EmailValidationLeg{Reason: models.MailProbeUnreachable, Detail: models.MailProbeDetailPrivateHost}
	verdict := models.EmailValidationVerdict{SMTP: leg, IMAP: leg}
	creds := &models.SmtpImap{SMTP: &models.Service{Host: "mail.corp.example", Port: 587}, IMAP: &models.Service{Host: "mail.corp.example", Port: 993}}

	t.Setenv("DEPLOYMENT_MODE", "cloud")
	xerr := validationError(verdict, creds)
	if xerr.Identifier != ErrIDMailboxUnreachable || !strings.Contains(xerr.Message, "outside your network") || strings.Contains(xerr.Message, "MAIL_ALLOW_PRIVATE_HOSTS") {
		t.Fatalf("hosted: %q", xerr.Message)
	}
	if strings.Count(xerr.Message, "outside your network") != 1 {
		t.Fatalf("hint repeated per leg: %q", xerr.Message)
	}

	t.Setenv("DEPLOYMENT_MODE", "self_hosted")
	if xerr := validationError(verdict, creds); !strings.Contains(xerr.Message, "MAIL_ALLOW_PRIVATE_HOSTS=true") {
		t.Fatalf("self-hosted: %q", xerr.Message)
	}
}

// TestValidationError_UntrustedCertificate: a certificate no authority vouches for offers the trust option, a missing STARTTLS does not.
func TestValidationError_UntrustedCertificate(t *testing.T) {
	ok := models.EmailValidationLeg{OK: true}
	cert := &models.MailCertificate{Fingerprint: strings.Repeat("ab", 32), Subject: "mail.corp.example", SelfSigned: true}
	untrusted := validationError(models.EmailValidationVerdict{SMTP: ok, IMAP: models.EmailValidationLeg{
		Reason: models.MailProbeTLS, Detail: "the server's certificate is not one a recognised certificate authority vouches for", Certificate: cert}}, gmailCreds())
	if untrusted.Identifier != ErrIDMailboxTLSFailed || untrusted.Reason != ReasonCertificateUntrusted || !strings.Contains(untrusted.Message, TrustCertificateHint) {
		t.Fatalf("untrusted: %q", untrusted.Message)
	}
	noTLS := validationError(models.EmailValidationVerdict{IMAP: ok, SMTP: models.EmailValidationLeg{
		Reason: models.MailProbeTLS, Detail: "the server offers no STARTTLS on this port"}}, gmailCreds())
	if strings.Contains(noTLS.Message, TrustCertificateHint) || noTLS.Reason != "" {
		t.Fatalf("no STARTTLS offered the trust option: %q", noTLS.Message)
	}
}
