package config

import (
	"strings"
	"testing"
)

func TestAuthRotationDefaultsPreserveSelfHosts(t *testing.T) {
	t.Setenv("AUTH_SECRET", strings.Repeat("a", 32))
	for _, name := range []string{"TWOFA_SECRET", "TWOFA_READ_SECRETS", "UNSUBSCRIBE_SIGNING_SECRET", "UNSUBSCRIBE_SIGNING_READ_SECRETS", "UNSUBSCRIBE_SIGNING_READ_UNTIL", "DOMAIN_PROOF_SECRET"} {
		t.Setenv(name, "")
	}
	t.Cleanup(func() { SetTurnstileSecretResolved(false) })
	cfg, err := (&Config{}).LoadAuthConfig(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TwoFASecret != cfg.AuthSecret || cfg.DomainProofSecret != cfg.AuthSecret || cfg.UnsubscribeSigningSecret != cfg.AuthSecret || len(cfg.TwoFAReadSecrets) != 0 || len(cfg.UnsubscribeReadSecrets) != 0 {
		t.Fatal("unset optional settings must preserve the existing key configuration")
	}
	t.Setenv("TWOFA_SECRET", strings.Repeat("m", 32))
	t.Setenv("DOMAIN_PROOF_SECRET", strings.Repeat("d", 32))
	t.Setenv("UNSUBSCRIBE_SIGNING_SECRET", strings.Repeat("u", 32))
	cfg, err = (&Config{}).LoadAuthConfig(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AuthSecret != strings.Repeat("a", 32) || cfg.TwoFASecret != strings.Repeat("m", 32) || cfg.DomainProofSecret != strings.Repeat("d", 32) || cfg.UnsubscribeSigningSecret != strings.Repeat("u", 32) {
		t.Fatal("separate purpose keys must resolve independently")
	}
	t.Setenv("UNSUBSCRIBE_SIGNING_READ_SECRETS", `["private-previous-signing-secret"]`)
	if _, err := (&Config{}).LoadAuthConfig(t.Context()); err == nil || strings.Contains(err.Error(), "private-previous-signing-secret") {
		t.Fatal("previous signing keys require a cutoff and must not appear in errors")
	}
	t.Setenv("UNSUBSCRIBE_SIGNING_READ_UNTIL", "2026-01-01T00:00:00Z")
	if _, err := (&Config{}).LoadAuthConfig(t.Context()); err != nil {
		t.Fatal("an elapsed cutoff must not prevent current services from starting")
	}
}
