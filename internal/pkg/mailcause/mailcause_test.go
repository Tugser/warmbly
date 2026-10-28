package mailcause

import (
	"strings"
	"testing"

	"github.com/warmbly/warmbly/internal/models"
)

func ok(name string) Leg { return Leg{Name: name, OK: true} }

func refused(name, host, detail string) Leg {
	return Leg{Name: name, Reason: models.MailProbeAuthRefused, Detail: detail, Host: host}
}

func TestClassify(t *testing.T) {
	const appPW = "abcd efgh ijkl mnop"
	cases := []struct {
		name string
		in   Input
		want string
	}{
		{"google 534 5.7.9", Input{MailHost: "google_workspace", Password: appPW,
			SMTP: refused("SMTP", "smtp.gmail.com", "534-5.7.9 Application-specific password required. For more information, go to 534 5.7.9 https://support.google.com/mail/?p=InvalidSecondFactor d9443c01a7336-21ec2c3f0c2sm12345678a.12 - gsmtp"),
			IMAP: ok("IMAP")}, GoogleAppPasswordRequired},
		{"google imap alert app password", Input{SMTP: ok("SMTP"),
			IMAP: refused("IMAP", "imap.gmail.com", "[ALERT] Application-specific password required: https://support.google.com/accounts/answer/185833 (Failure)")}, GoogleAppPasswordRequired},
		{"google account password shape", Input{MailHost: "gmail", Password: "Hunter2!secret",
			SMTP: refused("SMTP", "smtp.gmail.com", "535-5.7.8 Username and Password not accepted. For more information, go to 535 5.7.8 https://support.google.com/mail/?p=BadCredentials - gsmtp"),
			IMAP: refused("IMAP", "imap.gmail.com", "[AUTHENTICATIONFAILED] Invalid credentials (Failure)")}, GoogleAppPasswordRequired},
		{"google bad app password", Input{MailHost: "google_workspace", Password: appPW,
			SMTP: refused("SMTP", "smtp.gmail.com", "535-5.7.8 Username and Password not accepted. For more information, go to 535 5.7.8 https://support.google.com/mail/?p=BadCredentials - gsmtp"),
			IMAP: ok("IMAP")}, GoogleBadCredentials},
		{"google bad app password from host alone", Input{Password: "abcdefghijklmnop",
			SMTP: ok("SMTP"), IMAP: refused("IMAP", "imap.gmail.com", "[AUTHENTICATIONFAILED] Invalid credentials (Failure)")}, GoogleBadCredentials},
		{"google imap disabled consumer", Input{MailHost: "gmail", Password: appPW, SMTP: ok("SMTP"),
			IMAP: Leg{Reason: models.MailProbeProtocol, Host: "imap.gmail.com", Detail: "[ALERT] Your account is not enabled for IMAP use. Please visit your Gmail settings page and enable your account for IMAP access. (Failure)"}}, GoogleIMAPDisabled},
		{"google imap disabled workspace", Input{MailHost: "google_workspace", Password: appPW, SMTP: ok("SMTP"),
			IMAP: refused("IMAP", "imap.gmail.com", "[ALERT] IMAP access is disabled for your domain. Please contact your domain administrator for questions about this feature. (Failure)")}, GoogleIMAPDisabled},
		{"google web login", Input{MailHost: "gmail", Password: appPW,
			SMTP: refused("SMTP", "smtp.gmail.com", "534-5.7.14 <https://accounts.google.com/signin/continue?sarp=1&scc=1&plt=AKgnsbsQ1x9rT2qQh7Yq3bZr> Please log in via your web browser and then try again. 534-5.7.14 Learn more at 534 5.7.14 https://support.google.com/mail/answer/78754 - gsmtp"),
			IMAP: refused("IMAP", "imap.gmail.com", "[ALERT] Please log in via your web browser: https://support.google.com/mail/accounts/answer/78754 (Failure)")}, GoogleWebLoginRequired},
		{"google too many logins", Input{MailHost: "gmail", Password: appPW,
			SMTP: Leg{Reason: models.MailProbeTemporary, Host: "smtp.gmail.com", Detail: "454-4.7.0 Too many login attempts, please try again later. For more information, go to 454 4.7.0 https://support.google.com/mail/answer/7126229 - gsmtp"},
			IMAP: ok("IMAP")}, TooManyLogins},
		{"microsoft smtp auth disabled", Input{MailHost: "microsoft365", IMAP: ok("IMAP"),
			SMTP: refused("SMTP", "smtp.office365.com", "535 5.7.139 Authentication unsuccessful, SmtpClientAuthentication is disabled for the Tenant. Visit https://aka.ms/smtp_auth_disabled for more information. [BN0PR04CA0011.namprd04.prod.outlook.com 2024-01-01T00:00:00.000Z 08DC0000000000]")}, MicrosoftSMTPAuthDisabled},
		{"microsoft smtp auth disabled mailbox", Input{IMAP: ok("IMAP"),
			SMTP: refused("SMTP", "smtp.office365.com", "535 5.7.139 Authentication unsuccessful, SmtpClientAuthentication is disabled for the Mailbox. Visit https://aka.ms/smtp_auth_disabled for more information.")}, MicrosoftSMTPAuthDisabled},
		{"microsoft 504 5.7.4", Input{MailHost: "microsoft365", IMAP: ok("IMAP"),
			SMTP: Leg{Reason: models.MailProbeProtocol, Host: "smtp.office365.com", Detail: "504 5.7.4 Unrecognized authentication type [MW4PR03CA0123.namprd03.prod.outlook.com]"}}, MicrosoftSMTPAuthDisabled},
		{"microsoft security defaults", Input{MailHost: "microsoft365", IMAP: ok("IMAP"),
			SMTP: refused("SMTP", "smtp.office365.com", "535 5.7.139 Authentication unsuccessful, user is locked by your organization's security defaults policy. Contact your administrator.")}, MicrosoftSecurityDefaults},
		{"microsoft conditional access", Input{MailHost: "microsoft365", IMAP: ok("IMAP"),
			SMTP: refused("SMTP", "smtp.office365.com", "535 5.7.139 Authentication unsuccessful, the request did not meet the criteria to be authenticated successfully. Contact your administrator.")}, MicrosoftConditionalAccess},
		{"microsoft basic auth smtp", Input{MailHost: "microsoft365", IMAP: ok("IMAP"),
			SMTP: refused("SMTP", "smtp.office365.com", "535 5.7.139 Authentication unsuccessful, basic authentication is disabled.")}, MicrosoftBasicAuthDisabled},
		{"microsoft imap authenticate failed", Input{MailHost: "microsoft365",
			SMTP: refused("SMTP", "smtp.office365.com", "535 5.7.139 Authentication unsuccessful, SmtpClientAuthentication is disabled for the Tenant."),
			IMAP: refused("IMAP", "outlook.office365.com", "AUTHENTICATE failed.")}, MicrosoftBasicAuthDisabled},
		{"outlook consumer imap login failed", Input{MailHost: "outlook", SMTP: ok("SMTP"),
			IMAP: Leg{Reason: models.MailProbeProtocol, Host: "outlook.office365.com", Detail: "LOGIN failed."}}, MicrosoftBasicAuthDisabled},
		{"microsoft bad credentials", Input{MailHost: "microsoft365", IMAP: ok("IMAP"),
			SMTP: refused("SMTP", "smtp.office365.com", "535 5.7.3 Authentication unsuccessful [SN4PR0501CA0054.namprd05.prod.outlook.com]")}, MicrosoftBadCredentials},
		{"microsoft credentials incorrect", Input{IMAP: ok("IMAP"),
			SMTP: refused("SMTP", "smtp.office365.com", "535 5.7.139 Authentication unsuccessful, the user credentials were incorrect.")}, MicrosoftBadCredentials},
		{"microsoft account locked", Input{MailHost: "microsoft365", IMAP: ok("IMAP"),
			SMTP: refused("SMTP", "smtp.office365.com", "535 5.7.139 Authentication unsuccessful, account locked.")}, AccountLocked},
		{"yahoo refused", Input{MailHost: "yahoo", SMTP: ok("SMTP"),
			IMAP: refused("IMAP", "imap.mail.yahoo.com", "[AUTHENTICATIONFAILED] LOGIN Invalid credentials")}, YahooAppPasswordRequired},
		{"yahoo too many bad auth", Input{MailHost: "yahoo", IMAP: ok("IMAP"),
			SMTP: refused("SMTP", "smtp.mail.yahoo.com", "535 5.7.0 (#AUTH005) Too many bad auth attempts.")}, TooManyLogins},
		{"aol refused", Input{SMTP: refused("SMTP", "smtp.aol.com", "535 5.7.0 (#AUTH005) Bad credentials"), IMAP: ok("IMAP")}, AOLAppPasswordRequired},
		{"icloud refused", Input{MailHost: "icloud", IMAP: ok("IMAP"),
			SMTP: refused("SMTP", "smtp.mail.me.com", "535 5.7.8 Error: authentication failed")}, ICloudAppPasswordRequired},
		{"zoho refused", Input{MailHost: "zoho", IMAP: ok("IMAP"),
			SMTP: refused("SMTP", "smtppro.zoho.eu", "535 Authentication Failed")}, ZohoAppPasswordRequired},
		{"zoho imap not enabled", Input{MailHost: "zoho", SMTP: ok("SMTP"),
			IMAP: Leg{Reason: models.MailProbeProtocol, Host: "imappro.zoho.com", Detail: "NO You are yet to enable IMAP for your account. Please contact your administrator."}}, IMAPDisabled},
		{"fastmail refused", Input{MailHost: "fastmail", SMTP: ok("SMTP"),
			IMAP: refused("IMAP", "imap.fastmail.com", "[AUTHENTICATIONFAILED] Authentication failed")}, FastmailAppPasswordRequired},
		{"yandex refused", Input{SMTP: refused("SMTP", "smtp.yandex.com", "535 5.7.8 Error: authentication failed: This user does not have access rights to this service"), IMAP: ok("IMAP")}, YandexAppPasswordRequired},
		{"dovecot generic", Input{MailHost: "other", SMTP: refused("SMTP", "mail.example.org", "535 5.7.8 Error: authentication failed: authentication failure"),
			IMAP: refused("IMAP", "mail.example.org", "[AUTHENTICATIONFAILED] Authentication failed.")}, AuthRefused},
		{"godaddy locked", Input{MailHost: "godaddy", IMAP: ok("IMAP"),
			SMTP: refused("SMTP", "smtpout.secureserver.net", "535 Authentication failed: account disabled")}, AccountLocked},
		{"suspended", Input{SMTP: ok("SMTP"),
			IMAP: refused("IMAP", "mail.example.org", "NO [UNAVAILABLE] Your account has been suspended")}, AccountLocked},
		{"limit tag", Input{SMTP: ok("SMTP"),
			IMAP: Leg{Reason: models.MailProbeTemporary, Host: "mail.example.org", Detail: "NO [LIMIT] Too many simultaneous connections"}}, TooManyLogins},
		{"port 465 timeout", Input{IMAP: Leg{Reason: models.MailProbeTimeout, Port: 993},
			SMTP: Leg{Reason: models.MailProbeTimeout, Host: "mail.example.org", Port: 465}}, PortBlocked},
		{"port 25 unreachable imap ok", Input{IMAP: ok("IMAP"),
			SMTP: Leg{Reason: models.MailProbeUnreachable, Host: "mail.example.org", Port: 25, Detail: "dial tcp: i/o timeout"}}, PortBlocked},
		{"587 unreachable is not a blocked port", Input{IMAP: ok("IMAP"),
			SMTP: Leg{Reason: models.MailProbeUnreachable, Host: "mail.example.org", Port: 587, Detail: "connect: connection refused"}}, HostUnreachable},
		{"private host", Input{
			SMTP: Leg{Reason: models.MailProbeUnreachable, Host: "mail.corp.example", Port: 25, Detail: models.MailProbeDetailPrivateHost},
			IMAP: Leg{Reason: models.MailProbeUnreachable, Host: "mail.corp.example", Port: 993, Detail: models.MailProbeDetailPrivateHost}}, PrivateHost},
		{"no such host", Input{IMAP: ok("IMAP"),
			SMTP: Leg{Reason: models.MailProbeUnreachable, Host: "smtp.exmaple.org", Port: 465, Detail: "dial tcp: lookup smtp.exmaple.org: no such host"}}, HostNotFound},
		{"tls", Input{IMAP: ok("IMAP"),
			SMTP: Leg{Reason: models.MailProbeTLS, Port: 465, Detail: "tls: first record does not look like a TLS handshake"}}, TLSFailed},
		{"cleartext reason", Input{IMAP: Leg{Reason: models.MailProbeCleartext}, SMTP: ok("SMTP")}, CleartextRefused},
		{"starttls first", Input{IMAP: ok("IMAP"),
			SMTP: Leg{Reason: models.MailProbeProtocol, Detail: "530 5.7.0 Must issue a STARTTLS command first"}}, CleartextRefused},
		{"auth unavailable", Input{IMAP: ok("IMAP"),
			SMTP: Leg{Reason: models.MailProbeProtocol, Host: "mx.example.org", Port: 25, Detail: "smtp: server doesn't support AUTH; 503 5.5.1 Error: authentication not enabled"}}, AuthUnavailable},
		{"no supported sign-in method", Input{IMAP: ok("IMAP"),
			SMTP: Leg{Reason: models.MailProbeProtocol, Host: "mail.example.org", Port: 587, Detail: "the server offers no sign-in method Warmbly supports: it offers XOAUTH2, and Warmbly signs in with LOGIN, PLAIN or CRAM-MD5"}}, AuthUnavailable},
		{"timeout both", Input{IMAP: Leg{Reason: models.MailProbeTimeout, Port: 993}, SMTP: Leg{Reason: models.MailProbeTimeout, Port: 587}}, Timeout},
		{"temporary", Input{IMAP: ok("IMAP"), SMTP: Leg{Reason: models.MailProbeTemporary, Detail: "451 4.3.0 Temporary server error"}}, Temporary},
		{"auth outranks timeout", Input{MailHost: "other", IMAP: Leg{Reason: models.MailProbeTimeout},
			SMTP: refused("SMTP", "mail.example.org", "535 Incorrect authentication data")}, AuthRefused},
		{"protocol generic", Input{IMAP: ok("IMAP"), SMTP: Leg{Reason: models.MailProbeProtocol, Detail: "502 5.5.2 Error: command not recognized"}}, ServerDeclined},
		{"empty verdict", Input{}, ServerDeclined},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(tc.in)
			if got.Key != tc.want {
				t.Fatalf("got %q, want %q", got.Key, tc.want)
			}
			if got.Title == "" || got.Fix == "" {
				t.Fatalf("cause %q has no text", got.Key)
			}
		})
	}
}

func TestClassifyPassed(t *testing.T) {
	if c := Classify(Input{SMTP: ok("SMTP"), IMAP: ok("IMAP")}); c.Key != "" {
		t.Fatalf("a passing connect has no cause, got %q", c.Key)
	}
}

func TestClassifyNeverLeaksPassword(t *testing.T) {
	pw := "Sup3rSecretPassw0rd"
	c := Classify(Input{MailHost: "gmail", Password: pw, SMTP: refused("SMTP", "smtp.gmail.com", "535-5.7.8 Username and Password not accepted")})
	if strings.Contains(c.Title+c.Fix+c.Key, pw) {
		t.Fatal("password leaked")
	}
}

func TestCatalogue(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range Keys() {
		if seen[k] {
			t.Errorf("duplicate key %q", k)
		}
		seen[k] = true
		c, ok := Lookup(k)
		if !ok || c.Key != k || c.Title == "" || c.Fix == "" {
			t.Errorf("%q has no complete catalogue entry", k)
		}
		if strings.ContainsRune(c.Title+c.Fix, '—') {
			t.Errorf("%q uses an em dash", k)
		}
		if len(c.Fix) > 420 {
			t.Errorf("%q fix is %d chars", k, len(c.Fix))
		}
	}
	if _, ok := Lookup("nope"); ok {
		t.Fatal("unknown key found")
	}
	if !Generic(AuthRefused) || !Generic(ServerDeclined) || Generic(GoogleBadCredentials) {
		t.Fatal("Generic")
	}
}

func TestScrub(t *testing.T) {
	cases := []struct {
		in       string
		want     string
		mustDrop []string
	}{
		{in: "535 5.7.8 Authentication failed for bob@example.com", want: "535 5.7.8 Authentication failed for [address]"},
		{in: "Connection from 203.0.113.9 refused", want: "Connection from [ip] refused"},
		{in: "client [2001:db8::1] blocked", want: "client [[ip]] blocked"},
		{in: "at 12:30:45 today", want: "at 12:30:45 today"},
		{in: "535 5.7.139 Authentication unsuccessful, SmtpClientAuthentication is disabled for the Tenant. Visit https://aka.ms/smtp_auth_disabled for more information.",
			want: "535 5.7.139 Authentication unsuccessful, SmtpClientAuthentication is disabled for the Tenant. Visit https://aka.ms/smtp_auth_disabled for more information."},
		{in: "535-5.7.8 Username and Password not accepted. https://support.google.com/mail/?p=BadCredentials d9443c01a7336-21ec2c3f0c2sm12345678a.12 - gsmtp",
			mustDrop: []string{"d9443c01a7336"}},
		{in: "session 0123456789abcdef0123 closed", want: "session [token] closed"},
		{in: "token dGhpcyBpcyBhIHNlY3JldCB0b2tlbjEyMw== rejected", want: "token [token] rejected"},
		{in: "LOGIN failed for user 'jsmith'", want: "LOGIN failed for user [user]"},
		{in: "auth failed: user=jsmith, rip=10.0.0.1", mustDrop: []string{"jsmith", "10.0.0.1"}},
		{in: "authentication failed for \"j.smith\"", want: "authentication failed for [user]"},
		{in: "line one\r\nline two\x00‮", want: "line one line two"},
		{in: "535 Authentication Failed", want: "535 Authentication Failed"},
	}
	for _, tc := range cases {
		got := Scrub(tc.in)
		if tc.want != "" && got != tc.want {
			t.Errorf("Scrub(%q) = %q, want %q", tc.in, got, tc.want)
		}
		for _, s := range tc.mustDrop {
			if strings.Contains(got, s) {
				t.Errorf("Scrub(%q) = %q still contains %q", tc.in, got, s)
			}
		}
	}
	long := strings.Repeat("word ", 300)
	if got := Scrub(long); len([]rune(got)) > maxScrubbed+3 {
		t.Errorf("not capped: %d", len(got))
	}
}
