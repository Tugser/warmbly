package config

import "testing"

// A secret resolved from a secret store turns captcha on as surely as one in the environment.
func TestCaptchaProviderFollowsTheResolvedSecret(t *testing.T) {
	t.Setenv("CAPTCHA_PROVIDER", "")
	t.Setenv("TURNSTILE_SECRET", "")
	t.Cleanup(func() { SetTurnstileSecretResolved(false) })

	SetTurnstileSecretResolved(false)
	if got := CaptchaProvider(); got != "none" {
		t.Fatalf("no secret anywhere: got %q, want none", got)
	}
	SetTurnstileSecretResolved(true)
	if got := CaptchaProvider(); got != "turnstile" {
		t.Fatalf("secret from a secret store: got %q, want turnstile", got)
	}
}

func TestMailvendorSandboxURLOnlyInDevelopment(t *testing.T) {
	t.Setenv("MAILVENDOR_SANDBOX_URL", "http://127.0.0.1:18099/")
	for env, want := range map[string]string{"": "http://127.0.0.1:18099", "dev": "http://127.0.0.1:18099", "prod": "", "production": "", "staging": ""} {
		t.Setenv("APP_ENV", env)
		if got := MailvendorSandboxURL(); got != want {
			t.Errorf("APP_ENV=%q: got %q, want %q", env, got, want)
		}
	}
}

// Hosted production ignores both opt-outs; a self-hosted or development instance honours them.
func TestPrivateAddressOptOutsFollowTheDeployment(t *testing.T) {
	for _, tc := range []struct {
		mode, appEnv, flag           string
		webhooks, mailHosts, mailTLS bool
	}{
		{"cloud", "prod", "true", false, false, false},
		{"cloud", "prod", "", false, false, false},
		{"cloud", "dev", "true", true, true, true},
		{"cloud", "dev", "", false, true, false},
		{"self_hosted", "prod", "true", true, true, true},
		{"self_hosted", "prod", "", false, true, false},
		{"self_hosted", "prod", "false", false, false, false},
	} {
		t.Setenv("DEPLOYMENT_MODE", tc.mode)
		t.Setenv("APP_ENV", tc.appEnv)
		t.Setenv("WARMBLY_ALLOW_UNSAFE_WEBHOOK_URLS", tc.flag)
		t.Setenv("MAIL_ALLOW_PRIVATE_HOSTS", tc.flag)
		t.Setenv("MAIL_TLS_INSECURE", tc.flag)
		t.Setenv("SMTP_TLS_INSECURE_SKIP_VERIFY", tc.flag)
		if got := UnsafeWebhookURLsAllowed(); got != tc.webhooks {
			t.Errorf("%s/%s flag=%q: webhooks = %v, want %v", tc.mode, tc.appEnv, tc.flag, got, tc.webhooks)
		}
		if got := PrivateMailHostsAllowed(); got != tc.mailHosts {
			t.Errorf("%s/%s flag=%q: mail hosts = %v, want %v", tc.mode, tc.appEnv, tc.flag, got, tc.mailHosts)
		}
		if got := MailTLSInsecureAllowed(); got != tc.mailTLS {
			t.Errorf("%s/%s flag=%q: mail TLS insecure = %v, want %v", tc.mode, tc.appEnv, tc.flag, got, tc.mailTLS)
		}
		if got := SMTPTLSInsecureAllowed(); got != tc.mailTLS {
			t.Errorf("%s/%s flag=%q: SMTP TLS insecure = %v, want %v", tc.mode, tc.appEnv, tc.flag, got, tc.mailTLS)
		}
	}
}

// The platform relay verifies its certificate on hosted production whatever SMTP_TLS_INSECURE_SKIP_VERIFY says.
func TestSMTPConfigVerifiesCertificatesOnHostedProduction(t *testing.T) {
	t.Setenv("SMTP_HOST", "relay.example.com")
	t.Setenv("SMTP_TLS_INSECURE_SKIP_VERIFY", "true")
	for _, tc := range []struct {
		mode, appEnv string
		skip         bool
	}{
		{"cloud", "prod", false},
		{"self_hosted", "prod", true},
		{"cloud", "dev", true},
	} {
		t.Setenv("DEPLOYMENT_MODE", tc.mode)
		t.Setenv("APP_ENV", tc.appEnv)
		if got := (&Config{}).LoadSMTPConfig(t.Context()).InsecureSkipVerify; got != tc.skip {
			t.Errorf("%s/%s: InsecureSkipVerify = %v, want %v", tc.mode, tc.appEnv, got, tc.skip)
		}
	}
}
