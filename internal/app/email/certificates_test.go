package email

import (
	"strings"
	"testing"

	"github.com/warmbly/warmbly/internal/models"
)

func TestAdoptProbedCertificate(t *testing.T) {
	fp := strings.Repeat("ab", 32)
	cases := []struct {
		name           string
		in             models.Service
		leg            models.EmailValidationLeg
		wantUnverified bool
		wantPin        string
	}{
		{"verified server keeps no pin", models.Service{TLSPin: fp}, models.EmailValidationLeg{Certificate: &models.MailCertificate{Fingerprint: fp}}, false, ""},
		{"opted out records what the worker saw", models.Service{TLSUnverified: true}, models.EmailValidationLeg{Certificate: &models.MailCertificate{Fingerprint: strings.ToUpper(fp)}}, true, fp},
		{"a CA-valid chain drops the opt-out", models.Service{TLSUnverified: true, TLSPin: fp}, models.EmailValidationLeg{Certificate: &models.MailCertificate{Fingerprint: fp, Trusted: true}}, false, ""},
		{"no certificate reported drops the opt-out", models.Service{TLSUnverified: true}, models.EmailValidationLeg{}, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := tc.in
			adoptProbedCertificate(&svc, tc.leg)
			if svc.TLSUnverified != tc.wantUnverified || svc.TLSPin != tc.wantPin {
				t.Fatalf("got unverified=%v pin=%q", svc.TLSUnverified, svc.TLSPin)
			}
		})
	}
}

func TestInheritTLSKeepsTheOptOutForTheSameHost(t *testing.T) {
	fp := strings.Repeat("cd", 32)
	same := &models.Service{Host: "Mail.Example.com"}
	inheritTLS(same, "mail.example.com", true, fp)
	if !same.TLSUnverified || same.TLSPin != fp {
		t.Fatalf("same host: %+v", same)
	}
	moved := &models.Service{Host: "mail.other.net"}
	inheritTLS(moved, "mail.example.com", true, fp)
	if moved.TLSUnverified || moved.TLSPin != "" {
		t.Fatalf("new host inherited the pin: %+v", moved)
	}
}
