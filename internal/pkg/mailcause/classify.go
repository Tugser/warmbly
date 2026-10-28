package mailcause

import (
	"net"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/pkg/mailhost"
)

// Leg is one side of a connect attempt.
type Leg struct {
	Name   string // "SMTP" or "IMAP"
	OK     bool
	Reason string // models.MailProbe*
	Detail string
	Host   string
	Port   int
}

// Input is a failed connect. Password is read for its shape only and never
// appears in a Cause.
type Input struct {
	MailHost string
	SMTP     Leg
	IMAP     Leg
	Password string
}

// reasonRank matches the connect form's: the leg that says the most leads.
var reasonRank = map[string]int{
	models.MailProbeAuthRefused: 6,
	models.MailProbeTLS:         5,
	models.MailProbeUnreachable: 4,
	models.MailProbeCleartext:   3,
	models.MailProbeProtocol:    2,
	models.MailProbeTemporary:   1,
	models.MailProbeTimeout:     0,
}

// Classify names the cause of a failed connect. Rules run from the most
// specific server wording to the probe reason; a connect where both legs
// passed returns the zero Cause.
func Classify(in Input) Cause {
	if in.SMTP.OK && in.IMAP.OK {
		return Cause{}
	}
	return must(classify(in))
}

func classify(in Input) string {
	fam := family(in)
	var failed []Leg
	for _, l := range []Leg{in.SMTP, in.IMAP} {
		if !l.OK {
			failed = append(failed, l)
		}
	}
	var sb strings.Builder
	for _, l := range failed {
		sb.WriteString(strings.ToLower(l.Detail))
		sb.WriteByte('\n')
	}
	text := sb.String()
	google := fam.Google() || hasAny(text, "gsmtp", "support.google.com", "accounts.google.com")
	microsoft := fam.Microsoft() || hasAny(text, "outlook.com", "office365.com", "aka.ms/")
	imapAuthRefused := !in.IMAP.OK && (in.IMAP.Reason == models.MailProbeAuthRefused ||
		hasAny(strings.ToLower(in.IMAP.Detail), "authenticate failed", "login failed"))
	anyAuthRefused := (!in.SMTP.OK && in.SMTP.Reason == models.MailProbeAuthRefused) || imapAuthRefused

	// Exchange Online refuses a password over IMAP whatever else is wrong, so no SMTP fix connects it.
	if microsoft && imapAuthRefused {
		return MicrosoftBasicAuthDisabled
	}
	if microsoft {
		switch {
		case has(text, "smtpclientauthentication is disabled"):
			return MicrosoftSMTPAuthDisabled
		case has(text, "security defaults"):
			return MicrosoftSecurityDefaults
		case hasAny(text, "did not meet the criteria", "conditional access"):
			return MicrosoftConditionalAccess
		case has(text, "basic authentication is disabled"):
			return MicrosoftBasicAuthDisabled
		case hasAny(text, "504 5.7.4", "unrecognized authentication type"):
			return MicrosoftSMTPAuthDisabled
		}
	}

	if google {
		switch {
		case hasAny(text, "application-specific password required", "5.7.9", "invalidsecondfactor"):
			return GoogleAppPasswordRequired
		case hasAny(text, "5.7.14", "log in via your web browser", "web login required", "weblogin"):
			return GoogleWebLoginRequired
		case hasAny(text, "not enabled for imap", "imap access is disabled"):
			return GoogleIMAPDisabled
		}
	} else if has(text, "application-specific password required") {
		if k := appPasswordKey(fam); k != "" {
			return k
		}
	}
	if imapDisabled(text) {
		if google {
			return GoogleIMAPDisabled
		}
		return IMAPDisabled
	}

	switch {
	case locked(text):
		return AccountLocked
	case tooMany(text):
		return TooManyLogins
	case hasAny(text, "must issue a starttls", "must issue starttls", "starttls first", "requires tls", "encryption required", "must use tls"):
		return CleartextRefused
	case has(text, "private network address"):
		return PrivateHost
	case hasAny(text, "no such host", "nxdomain", "name or service not known"):
		return HostNotFound
	case hasAny(text, "no supported authentication", "no sign-in method warmbly supports", "auth not supported", "authentication not supported",
		"authentication not enabled", "unrecognized authentication type", "does not support auth",
		"authentication mechanism not supported", "auth not available", "authentication not available"):
		return AuthUnavailable
	}

	if google && (anyAuthRefused || hasAny(text, "5.7.8", "username and password not accepted", "badcredentials")) {
		if in.Password != "" && !mailhost.LooksLikeGoogleAppPassword(in.Password) {
			return GoogleAppPasswordRequired
		}
		return GoogleBadCredentials
	}
	if microsoft && (anyAuthRefused || hasAny(text, "5.7.3", "5.7.139", "credentials were incorrect")) {
		return MicrosoftBadCredentials
	}
	if anyAuthRefused {
		if k := appPasswordKey(fam); k != "" {
			return k
		}
	}

	if !in.SMTP.OK && portBlocked(in) {
		return PortBlocked
	}

	lead := failed[0]
	for _, l := range failed[1:] {
		if reasonRank[l.Reason] > reasonRank[lead.Reason] {
			lead = l
		}
	}
	switch lead.Reason {
	case models.MailProbeAuthRefused:
		return AuthRefused
	case models.MailProbeUnreachable:
		return HostUnreachable
	case models.MailProbeTLS:
		return TLSFailed
	case models.MailProbeCleartext:
		return CleartextRefused
	case models.MailProbeTemporary:
		return Temporary
	case models.MailProbeTimeout:
		return Timeout
	}
	return ServerDeclined
}

// family resolves the provider from the stored mail_host, then from the servers.
func family(in Input) mailhost.Host {
	if h := mailhost.Host(in.MailHost); h != mailhost.Unknown && h != mailhost.Other && mailhost.Valid(in.MailHost) {
		return h
	}
	if h := mailhost.FromServer(in.SMTP.Host); h != mailhost.Unknown {
		return h
	}
	return mailhost.FromServer(in.IMAP.Host)
}

// appPasswordKey is the cause for a refused password on a host that wants an app password.
func appPasswordKey(h mailhost.Host) string {
	switch h {
	case mailhost.GoogleWorkspace, mailhost.Gmail:
		return GoogleAppPasswordRequired
	case mailhost.Yahoo:
		return YahooAppPasswordRequired
	case mailhost.AOL:
		return AOLAppPasswordRequired
	case mailhost.ICloud:
		return ICloudAppPasswordRequired
	case mailhost.Zoho:
		return ZohoAppPasswordRequired
	case mailhost.Fastmail:
		return FastmailAppPasswordRequired
	case mailhost.Yandex:
		return YandexAppPasswordRequired
	}
	return ""
}

// portBlocked: SMTP stayed silent on 465, or on 25/465 while IMAP signed in.
func portBlocked(in Input) bool {
	silent := in.SMTP.Reason == models.MailProbeTimeout || in.SMTP.Reason == models.MailProbeUnreachable
	if !silent || has(strings.ToLower(in.SMTP.Detail), "no such host") {
		return false
	}
	if in.SMTP.Reason == models.MailProbeTimeout && in.SMTP.Port == 465 {
		return true
	}
	return in.IMAP.OK && (in.SMTP.Port == 25 || in.SMTP.Port == 465)
}

func imapDisabled(text string) bool {
	return hasAny(text, "imap access is disabled", "imap is disabled", "imap access disabled",
		"not enabled for imap", "imap is not enabled", "yet to enable imap", "imap access not enabled",
		"imap not enabled", "enable imap access")
}

func locked(text string) bool {
	return hasAny(text, "account is locked", "account locked", "account has been locked", "account is temporarily locked",
		"account is disabled", "account disabled", "account has been disabled", "account suspended",
		"account is suspended", "account has been suspended", "account is blocked", "account has been blocked",
		"user is disabled", "mailbox is disabled", "mailbox disabled", "account deactivated",
		"account has been deactivated", "account is deactivated", "user account is locked")
}

func tooMany(text string) bool {
	return hasAny(text, "454 4.7.0", "454-4.7.0", "too many login", "too many bad auth", "too many auth",
		"too many failed", "too many invalid", "too many simultaneous", "too many connections",
		"[limit]", "rate limit", "ratelimit", "rate-limit", "rate limited", "throttl")
}

func has(s, sub string) bool { return strings.Contains(s, sub) }

func hasAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

const maxScrubbed = 500

var (
	emailRe = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9\-]+(?:\.[A-Za-z0-9\-]+)+`)
	ipv4Re  = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	ipv6Re  = regexp.MustCompile(`[0-9A-Fa-f]{0,4}(?::[0-9A-Fa-f]{0,4}){2,7}(?:%[0-9A-Za-z]+)?`)
	hexRe   = regexp.MustCompile(`\b[0-9A-Fa-f]{16,}\b`)
	tokenRe = regexp.MustCompile(`[A-Za-z0-9+/=_\-]{20,}`)
	// userRe catches "user=bob", "username: bob", "login=bob".
	userRe = regexp.MustCompile(`(?i)\b(user(?:name)?|login|account|mailbox|uid|authzid|authcid)(\s*[=:]\s*)("[^"]*"|'[^']*'|\S+)`)
	// quotedUserRe catches "for user 'bob'" and "user \"bob\"".
	quotedUserRe = regexp.MustCompile(`(?i)\b(user(?:name)?|for|account|mailbox)(\s+)("[^"]{1,128}"|'[^']{1,128}'|<[^>]{1,128}>)`)
)

// Scrub removes addresses, IPs, long tokens and usernames from a server's
// reply so it can be stored and shown without leaking who or what signed in.
func Scrub(detail string) string {
	s := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, detail)
	s = emailRe.ReplaceAllString(s, "[address]")
	s = userRe.ReplaceAllString(s, "$1$2[user]")
	s = quotedUserRe.ReplaceAllString(s, "$1$2[user]")
	s = ipv6Re.ReplaceAllStringFunc(s, func(m string) string {
		if net.ParseIP(strings.SplitN(m, "%", 2)[0]) != nil {
			return "[ip]"
		}
		return m
	})
	s = ipv4Re.ReplaceAllStringFunc(s, func(m string) string {
		if net.ParseIP(m) != nil {
			return "[ip]"
		}
		return m
	})
	s = hexRe.ReplaceAllString(s, "[token]")
	s = tokenRe.ReplaceAllStringFunc(s, func(m string) string {
		if strings.ContainsAny(m, "0123456789") && strings.IndexFunc(m, unicode.IsLetter) >= 0 {
			return "[token]"
		}
		return m
	})
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > maxScrubbed {
		r := []rune(s)
		s = string(r[:maxScrubbed]) + "..."
	}
	return s
}
