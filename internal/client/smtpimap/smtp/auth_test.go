package smtp

import (
	"errors"
	"net"
	"net/smtp"
	"strings"
	"testing"
)

func TestChooseAuthPicksByLinkAndAdvertisement(t *testing.T) {
	for _, tc := range []struct {
		name       string
		advertised string
		encrypted  bool
		host       string
		wantMech   string
		wantErr    error
	}{
		{"tls login only", "LOGIN", true, "mail.example.com", "LOGIN", nil},
		{"tls plain only", "PLAIN", true, "mail.example.com", "PLAIN", nil},
		{"tls plain and login", "PLAIN LOGIN", true, "mail.example.com", "LOGIN", nil},
		{"tls cram preferred last", "CRAM-MD5 PLAIN LOGIN", true, "mail.example.com", "LOGIN", nil},
		{"tls cram with plain", "CRAM-MD5 PLAIN", true, "mail.example.com", "PLAIN", nil},
		{"tls cram only", "CRAM-MD5", true, "mail.example.com", "CRAM-MD5", nil},
		{"tls lower case", "login plain", true, "mail.example.com", "LOGIN", nil},
		{"tls nothing usable", "GSSAPI NTLM", true, "mail.example.com", "", ErrSMTPNoSupportedAuth},
		{"cleartext login refused", "LOGIN", false, "mail.example.com", "", ErrSMTPCleartextAuth},
		{"cleartext plain refused", "PLAIN", false, "mail.example.com", "", ErrSMTPCleartextAuth},
		{"cleartext cram refused", "CRAM-MD5", false, "mail.example.com", "", ErrSMTPCleartextAuth},
		{"loopback cleartext login", "LOGIN PLAIN", false, "127.0.0.1", "LOGIN", nil},
		{"loopback cleartext plain", "PLAIN", false, "localhost", "PLAIN", nil},
		{"loopback cleartext cram", "CRAM-MD5", false, "::1", "CRAM-MD5", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth, err := chooseAuth(tc.advertised, tc.encrypted, "user@example.com", "pw", tc.host)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			mech, _, err := auth.Start(&smtp.ServerInfo{Name: tc.host, TLS: tc.encrypted})
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			if mech != tc.wantMech {
				t.Fatalf("mechanism = %q, want %q", mech, tc.wantMech)
			}
		})
	}
}

func TestLoginAuthRefusesCleartextToARemoteHost(t *testing.T) {
	auth := NewLoginAuth("user", "pw", "mail.example.com")
	if _, _, err := auth.Start(&smtp.ServerInfo{Name: "mail.example.com"}); !errors.Is(err, ErrSMTPCleartextAuth) {
		t.Fatalf("err = %v, want ErrSMTPCleartextAuth", err)
	}
}

// The XOAUTH2 bearer token travels only over TLS or to this machine.
func TestOAuth2AuthRefusesCleartextToARemoteHost(t *testing.T) {
	auth := newOAuth2Auth("sender", "ya29.token")
	for _, tc := range []struct {
		server *smtp.ServerInfo
		ok     bool
	}{
		{&smtp.ServerInfo{Name: "smtp.gmail.com"}, false},
		{nil, false},
		{&smtp.ServerInfo{Name: "smtp.gmail.com", TLS: true}, true},
		{&smtp.ServerInfo{Name: net.IPv4(127, 0, 0, 1).String()}, true},
		{&smtp.ServerInfo{Name: "::1"}, true},
		{&smtp.ServerInfo{Name: "localhost"}, true},
	} {
		mech, payload, err := auth.Start(tc.server)
		if !tc.ok {
			if !errors.Is(err, ErrSMTPCleartextAuth) || payload != nil {
				t.Fatalf("%+v: err = %v, payload sent = %v; want ErrSMTPCleartextAuth and no payload", tc.server, err, payload != nil)
			}
			continue
		}
		if err != nil || mech != "XOAUTH2" || !strings.Contains(string(payload), "auth=Bearer ya29.token") {
			t.Fatalf("%+v: mech=%q err=%v", tc.server, mech, err)
		}
	}
}
