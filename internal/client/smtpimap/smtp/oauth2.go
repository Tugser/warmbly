package smtp

import (
	"fmt"
	"net/smtp"
)

// ---------- XOAUTH2 Implementation ----------

type oauth2Auth struct {
	username, accessToken string
}

func newOAuth2Auth(user, token string) smtp.Auth {
	return &oauth2Auth{user, token}
}

// Start sends the bearer token only over TLS or to this machine, like the password mechanisms.
func (a *oauth2Auth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if server == nil || (!server.TLS && !IsLoopbackHost(server.Name)) {
		return "", nil, ErrSMTPCleartextAuth
	}
	payload := fmt.Sprintf("user=%s\x01auth=Bearer %s\x01\x01", a.username, a.accessToken)
	return "XOAUTH2", []byte(payload), nil
}

func (a *oauth2Auth) Next(fromServer []byte, more bool) ([]byte, error) {
	if more {
		return nil, fmt.Errorf("unexpected server challenge: %s", string(fromServer))
	}
	return nil, nil
}
