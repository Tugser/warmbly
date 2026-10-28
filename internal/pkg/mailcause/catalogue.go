// Package mailcause turns a failed mailbox connect into a stable cause key
// with a short title and a concrete fix, so an import can group its failures
// by what the person has to do about them.
package mailcause

// Cause is one kind of connect failure and what fixes it.
type Cause struct {
	Key   string `json:"cause"`
	Title string `json:"title"`
	Fix   string `json:"fix"`
	// Retryable: the same settings can succeed on a later attempt or with a new password.
	Retryable bool `json:"retryable"`
}

// Cause keys.
const (
	GoogleAppPasswordRequired   = "google_app_password_required"
	GoogleBadCredentials        = "google_bad_credentials"
	GoogleIMAPDisabled          = "google_imap_disabled"
	GoogleWebLoginRequired      = "google_web_login_required"
	MicrosoftSMTPAuthDisabled   = "microsoft_smtp_auth_disabled"
	MicrosoftSecurityDefaults   = "microsoft_security_defaults"
	MicrosoftConditionalAccess  = "microsoft_conditional_access"
	MicrosoftBasicAuthDisabled  = "microsoft_basic_auth_disabled"
	MicrosoftBadCredentials     = "microsoft_bad_credentials"
	YahooAppPasswordRequired    = "yahoo_app_password_required"
	AOLAppPasswordRequired      = "aol_app_password_required"
	ICloudAppPasswordRequired   = "icloud_app_password_required"
	ZohoAppPasswordRequired     = "zoho_app_password_required"
	FastmailAppPasswordRequired = "fastmail_app_password_required"
	YandexAppPasswordRequired   = "yandex_app_password_required"
	IMAPDisabled                = "imap_disabled"
	AccountLocked               = "account_locked"
	TooManyLogins               = "too_many_logins"
	AuthUnavailable             = "auth_unavailable"
	AuthRefused                 = "auth_refused"
	HostNotFound                = "host_not_found"
	HostUnreachable             = "host_unreachable"
	PrivateHost                 = "private_host"
	PortBlocked                 = "port_blocked"
	TLSFailed                   = "tls_failed"
	CleartextRefused            = "cleartext_refused"
	Timeout                     = "timeout"
	Temporary                   = "temporary"
	ServerDeclined              = "server_declined"
)

var catalogue = []Cause{
	{GoogleAppPasswordRequired, "Google needs an app password",
		"Google does not accept the account password over IMAP or SMTP. Turn on 2-Step Verification, create an app password at https://myaccount.google.com/apppasswords while signed in to this address, and use the 16 letters it shows. On Google Workspace your admin must allow 2-Step Verification. Or connect with Google sign-in instead.", true},
	{GoogleBadCredentials, "Google did not accept the app password",
		"Google refused this address and app password. Create a new app password at https://myaccount.google.com/apppasswords while signed in to this exact address (not an alias or group) and use it. A deleted or revoked app password stops working at once.", true},
	{GoogleIMAPDisabled, "IMAP is turned off in Google",
		"Turn IMAP on for this mailbox: in Gmail open Settings, See all settings, Forwarding and POP/IMAP, and enable IMAP. On Google Workspace an admin must also allow it in the Admin console under Apps, Google Workspace, Gmail, End User Access, POP and IMAP access.", true},
	{GoogleWebLoginRequired, "Google wants a browser sign-in first",
		"Google blocked this sign-in as unusual. Sign in to this mailbox once at https://mail.google.com from a browser, confirm any security prompt, then retry. If Google shows a security alert for this sign-in, confirm it was you.", true},
	{MicrosoftSMTPAuthDisabled, "SMTP sign-in is turned off in Microsoft 365",
		"Your Microsoft 365 admin has SMTP AUTH turned off. Turn it on for this mailbox in the Microsoft 365 admin center (Active users, Mail, Manage email apps, Authenticated SMTP) or run Set-CASMailbox -Identity <address> -SmtpClientAuthenticationDisabled $false. Microsoft sign-in is the better path. GoDaddy-sold Microsoft 365 needs PowerShell or GoDaddy support.", true},
	{MicrosoftSecurityDefaults, "Microsoft security defaults block passwords",
		"Microsoft Entra security defaults block password sign-in over SMTP and IMAP for this tenant. Connect the mailbox with Microsoft sign-in instead. If you must use a password, an admin can turn security defaults off in the Entra admin center under Identity, Overview, Properties, Manage security defaults.", true},
	{MicrosoftConditionalAccess, "A Microsoft sign-in policy blocked this",
		"A Conditional Access or authentication policy in your Microsoft tenant refused this password sign-in. Connect the mailbox with Microsoft sign-in instead, or ask your admin to allow SMTP AUTH for this mailbox in the Entra admin center.", true},
	{MicrosoftBasicAuthDisabled, "Microsoft needs Microsoft sign-in",
		"Exchange Online no longer accepts a password over IMAP, so this mailbox cannot connect with one. Connect it with Microsoft sign-in instead: it takes one click per mailbox, and on some tenants an admin has to approve the app once.", false},
	{MicrosoftBadCredentials, "Microsoft did not accept the password",
		"Microsoft refused this address and password. Check both, including the sign-in address if it differs from the mailbox address. If the account uses MFA a password will not work here; connect with Microsoft sign-in instead.", true},
	{YahooAppPasswordRequired, "Yahoo needs an app password",
		"Yahoo only accepts an app password over IMAP and SMTP. Sign in to Yahoo, open Account Security at https://login.yahoo.com/account/security, choose Generate app password, and use the password it shows.", true},
	{AOLAppPasswordRequired, "AOL needs an app password",
		"AOL only accepts an app password over IMAP and SMTP. Sign in to AOL, open Account Security at https://login.aol.com/account/security, choose Generate app password, and use the password it shows.", true},
	{ICloudAppPasswordRequired, "iCloud needs an app-specific password",
		"iCloud Mail only accepts an app-specific password. Sign in at https://account.apple.com, open Sign-In and Security, App-Specific Passwords, create one, and use it. The username is the full iCloud address, without any alias.", true},
	{ZohoAppPasswordRequired, "Zoho refused the password",
		"Zoho refused this sign-in. If two-factor authentication is on, create an app-specific password in Zoho Accounts under Security, App Passwords, and use it. Also check that IMAP access is enabled in Zoho Mail settings under Mail Accounts, IMAP.", true},
	{FastmailAppPasswordRequired, "Fastmail needs an app password",
		"Fastmail only accepts an app password over IMAP and SMTP. In Fastmail open Settings, Privacy and Security, Manage app passwords, create one with IMAP and SMTP access, and use it.", true},
	{YandexAppPasswordRequired, "Yandex needs an app password",
		"Yandex only accepts an app password over IMAP and SMTP. Create one for Mail at https://id.yandex.com/security/app-passwords, and check that IMAP is allowed in Yandex Mail settings under Email clients.", true},
	{IMAPDisabled, "IMAP is turned off for this mailbox",
		"The server says IMAP access is disabled for this account. Turn IMAP on in the provider's mail settings (or ask your email admin to), then retry.", true},
	{AccountLocked, "The account is locked or disabled",
		"The provider says this account is locked, suspended or disabled. Sign in to it in a browser to see why and restore access, or ask your email admin, then retry.", false},
	{TooManyLogins, "Too many sign-in attempts",
		"The provider is limiting sign-ins for this account or from this network. Wait 15 to 60 minutes before retrying, and avoid retrying the same failing password repeatedly.", true},
	{AuthUnavailable, "The server does not take a password here",
		"The server did not offer a password sign-in method Warmbly supports (LOGIN, PLAIN or CRAM-MD5) on this port. Check the host and port: use the provider's submission server (usually port 587 or 465), not the MX server or port 25.", false},
	{AuthRefused, "The server refused the sign-in",
		"The server refused this username and password. Check both, and whether the provider needs an app password or a username other than the email address.", true},
	{HostNotFound, "The server name does not exist",
		"The server hostname could not be found in DNS. Check the spelling of the SMTP and IMAP host.", false},
	{HostUnreachable, "The server could not be reached",
		"No connection could be made to the server. Check the host and port, and that the server accepts connections from outside your network.", false},
	{PrivateHost, "The server is on a private network",
		"The server's host name points to a private network address, and Warmbly only connects mailboxes to public addresses. Use the host name your provider gives for connecting from outside your network.", false},
	{PortBlocked, "The mail port looks blocked",
		"The SMTP port did not answer while IMAP worked, which usually means the port is blocked on the way. Try port 587 with STARTTLS instead of 465 or 25, or check the provider's documented SMTP port.", false},
	{TLSFailed, "The secure connection failed",
		"The server did not complete a secure connection. Check the security setting for the port: 465 and 993 use SSL/TLS, 587 and 143 use STARTTLS. Also check that the hostname matches the server's certificate. A server with a self-signed or company certificate can be connected on its own certificate from the connect form.", false},
	{CleartextRefused, "Encryption is required",
		"Warmbly only signs in over an encrypted connection, and this one was not encrypted or the server only offers sign-in without encryption. Use SSL/TLS on 465 or 993, or STARTTLS on 587 or 143.", false},
	{Timeout, "The server did not answer in time",
		"The server did not answer before the time ran out. It may be slow or overloaded; retry later, and check the host and port if it keeps happening.", true},
	{Temporary, "The server asked to retry later",
		"The server declined the sign-in for now and asked for a retry. Retry in a few minutes.", true},
	{ServerDeclined, "The server did not accept the connection",
		"The server answered but did not accept the sign-in. Check the host, port, security setting and credentials, then retry.", true},
}

var byKey = func() map[string]Cause {
	m := make(map[string]Cause, len(catalogue))
	for _, c := range catalogue {
		m[c.Key] = c
	}
	return m
}()

// Lookup returns the catalogue entry for key.
func Lookup(key string) (Cause, bool) {
	c, ok := byKey[key]
	return c, ok
}

// Keys lists every cause key in catalogue order.
func Keys() []string {
	out := make([]string, len(catalogue))
	for i, c := range catalogue {
		out[i] = c.Key
	}
	return out
}

// Generic reports whether key is a catch-all a finer classifier may refine.
func Generic(key string) bool {
	return key == AuthRefused || key == ServerDeclined
}

func must(key string) Cause {
	c, ok := byKey[key]
	if !ok {
		return byKey[ServerDeclined]
	}
	return c
}
