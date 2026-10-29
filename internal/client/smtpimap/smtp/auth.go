package smtp

import (
	"errors"
	"fmt"
	"net/smtp"
	"strings"

	"github.com/warmbly/warmbly/internal/config"
	"github.com/warmbly/warmbly/internal/models"
)

// ErrSMTPCleartextAuth is returned rather than sending credentials over an
// unencrypted link. Go's own PlainAuth refuses the same thing, but silently
// enough that operators read it as a credentials problem.
var ErrSMTPCleartextAuth = errors.New("smtp: refusing to send credentials over an unencrypted connection")

// ErrSMTPNoSupportedAuth means the server advertised AUTH but none of the
// mechanisms we implement.
var ErrSMTPNoSupportedAuth = errors.New("smtp: the server advertises no supported authentication mechanism")

// NegotiateAuth picks a mechanism from what the server actually advertised,
// over the link the session is on.
//
// A password mechanism is only chosen over an established TLS session
// (implicit TLS or a completed STARTTLS), or to a loopback host, which is the
// only place the unencrypted mailbox mode reaches. Anywhere else the answer
// is ErrSMTPCleartextAuth, whatever the server offers.
//
// Over that link LOGIN and PLAIN come first: TLS already protects the
// password, and a server that speaks only one of the two speaks LOGIN
// (Exchange Online, most appliance relays), which is why it is negotiated
// rather than sent blind. Self-hosts may use CRAM-MD5 when neither is offered.
//
// A server that advertises no AUTH at all gets a nil Auth and no error: it
// wants no authentication, which is the local development sink.
func NegotiateAuth(client *smtp.Client, username, password, host string) (smtp.Auth, error) {
	ok, ext := client.Extension("AUTH")
	if !ok {
		return nil, nil
	}
	_, encrypted := client.TLSConnectionState()
	return chooseAuth(ext, encrypted, username, password, host)
}

// chooseAuth is NegotiateAuth's decision, given the advertised mechanisms and
// whether the session is encrypted.
func chooseAuth(advertised string, encrypted bool, username, password, host string) (smtp.Auth, error) {
	if !encrypted && !IsLoopbackHost(host) {
		return nil, ErrSMTPCleartextAuth
	}
	offered := map[string]bool{}
	for _, mech := range strings.Fields(strings.ToUpper(advertised)) {
		offered[mech] = true
	}
	switch {
	case offered["LOGIN"]:
		return NewLoginAuth(username, password, host), nil
	case offered["PLAIN"]:
		return smtp.PlainAuth("", username, password, host), nil
	case offered["CRAM-MD5"] && config.SMTPCRAMMD5Allowed():
		return smtp.CRAMMD5Auth(username, password), nil
	}
	return nil, fmt.Errorf("%w: %q", ErrSMTPNoSupportedAuth, advertised)
}

// LoginAuth implements the non-standard but widely required AUTH LOGIN
// mechanism, which net/smtp does not ship. Exchange Online and many appliance
// relays advertise it exclusively.
type LoginAuth struct {
	username string
	password string
	host     string
}

// NewLoginAuth returns an AUTH LOGIN mechanism for the given credentials.
func NewLoginAuth(username, password, host string) smtp.Auth {
	return &LoginAuth{username: username, password: password, host: host}
}

func (a *LoginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS && !IsLoopbackHost(server.Name) {
		return "", nil, ErrSMTPCleartextAuth
	}
	return "LOGIN", nil, nil
}

func (a *LoginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	// Servers vary in how they word the prompts, so match on the decoded
	// challenge rather than expecting an exact string.
	challenge := strings.ToLower(string(fromServer))
	switch challenge {
	case "username:", "user name":
		return []byte(a.username), nil
	case "password:":
		return []byte(a.password), nil
	}
	if strings.Contains(challenge, "user") {
		return []byte(a.username), nil
	}
	if strings.Contains(challenge, "pass") {
		return []byte(a.password), nil
	}
	return nil, fmt.Errorf("smtp: unexpected LOGIN challenge %q", string(fromServer))
}

// IsLoopbackHost reports whether host is this machine, where credentials
// cannot reach a wire even without TLS. One definition, shared with the
// mailbox security modes, so the AUTH guard and the "none" mode can never
// disagree about what counts as local.
func IsLoopbackHost(host string) bool {
	return models.LoopbackMailHost(host)
}
