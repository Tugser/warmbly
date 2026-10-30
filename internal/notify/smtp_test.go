package notify

import (
	"errors"
	"net/smtp"
	"testing"

	wsmtp "github.com/warmbly/warmbly/internal/client/smtpimap/smtp"
	"github.com/warmbly/warmbly/internal/config"
)

func TestExplicitCRAMMD5FollowsDeploymentPolicy(t *testing.T) {
	s := &smtpEmailNotificationService{cfg: &config.SMTPConfig{Auth: config.SMTPAuthCRAMMD5, Username: "user", Password: "password"}}
	for _, tc := range []struct {
		mode, env string
		allowed   bool
	}{
		{"cloud", "prod", false},
		{"cloud", "staging", false},
		{"self_hosted", "prod", true},
		{"cloud", "dev", true},
	} {
		t.Run(tc.mode+"/"+tc.env, func(t *testing.T) {
			t.Setenv("DEPLOYMENT_MODE", tc.mode)
			t.Setenv("APP_ENV", tc.env)
			auth, err := s.authMechanism(nil)
			if !tc.allowed {
				if auth != nil || !errors.Is(err, wsmtp.ErrSMTPNoSupportedAuth) {
					t.Fatalf("auth=%v err=%v", auth, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			mechanism, _, err := auth.Start(&smtp.ServerInfo{Name: "mail.example.com", TLS: true})
			if err != nil || mechanism != "CRAM-MD5" {
				t.Fatalf("mechanism=%q err=%v", mechanism, err)
			}
		})
	}
}
