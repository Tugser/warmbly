package instancecheck

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/warmbly/warmbly/internal/config"
	"github.com/warmbly/warmbly/internal/models"
)

const (
	docsRegistration = "/development/accounts-and-access/#registration-modes"
	docsSignIn       = "/development/accounts-and-access/#sign-in-methods"
	docsAdmins       = "/development/accounts-and-access/#platform-admins"
	docsFirstOwner   = "/development/accounts-and-access/#first-owner"
	docsInvitations  = "/development/accounts-and-access/#invitations"
)

// setupTokenKey mirrors the unexported key in internal/app/bootstrap. The two
// must stay identical or the outstanding-setup-link check goes silent.
const setupTokenKey = "bootstrap:setup_token"

func accessChecks() []check {
	return []check{
		{id: "registration_mode", run: checkRegistrationMode},
		{id: "no_sign_in_method", run: checkNoSignInMethod},
		{id: "google_sign_in_incomplete", run: checkGoogleSignInIncomplete},
		{id: "apple_sign_in_incomplete", run: checkAppleSignInIncomplete},
		{id: "single_platform_admin", run: checkSinglePlatformAdmin},
		{id: "bootstrap_password_still_set", run: checkBootstrapPasswordStillSet},
		{id: "setup_link_outstanding", run: checkSetupLinkOutstanding},
		{id: "expired_invitations", run: checkExpiredInvitations},
	}
}

func checkRegistrationMode(ctx context.Context, d Deps, in Input) *Finding {
	p := policy(d)
	var title, message string
	switch p.Registration {
	case config.RegistrationOpen:
		title = "Public registration is open"
		message = "DISABLE_REGISTRATION=false: anyone can create an account from the sign-up form. " +
			"Workspace invitations are also enabled. This is a normal registration mode, not a health problem."
	case config.RegistrationInviteOnly:
		title = "Registration is invitation-only"
		message = "DISABLE_REGISTRATION=invite_only: public signup is closed, but workspace invitations are enabled " +
			"from Settings > Members in the dashboard. Signup from an invitation also requires access.allow_invited_signup " +
			"in Instance > Configuration > Settings."
	case config.RegistrationClosed:
		title = "Registration is closed"
		message = "DISABLE_REGISTRATION=true: the sign-up form and workspace invitations are disabled. " +
			"An operator can still create accounts with warmblyctl user create."
	default:
		return nil
	}
	if p.SSOAutoProvision {
		message += " SSO_AUTO_PROVISION=true allows a verified single sign-on identity to create an account independently of this mode."
	}
	return result(CategoryAccess, SeverityInfo, title, message, docsRegistration)
}

func checkNoSignInMethod(ctx context.Context, d Deps, in Input) *Finding {
	if !policy(d).DisablePasswordLogin {
		return nil
	}
	if env("OIDC_ISSUER_URL") != "" || runtimeOf(d).OIDCConfigured {
		return nil
	}
	if googleSignInConfigured() || appleSignInConfigured() {
		return nil
	}
	return result(CategoryAccess, SeverityError, "No way to sign in",
		"Password login is disabled and no single sign-on provider is configured, so there is no way to sign in to this instance. "+
			"Set DISABLE_PASSWORD_LOGIN=false or configure OIDC_ISSUER_URL.",
		docsSignIn)
}

// googleSignInConfigured and appleSignInConfigured mirror what the backend
// actually requires to wire the provider. A client id on its own enables
// nothing, which is why "I set GOOGLE_CLIENT_ID and the button does not work"
// is the report this pair of checks exists to answer.
func googleSignInConfigured() bool {
	return env("GOOGLE_CLIENT_ID") != "" && env("GOOGLE_CLIENT_SECRET") != "" && ssoRedirectConfigured("GOOGLE_REDIRECT_URI")
}

func appleSignInConfigured() bool {
	return env("APPLE_APP_ID") != "" && env("APPLE_TEAM_ID") != "" && env("APPLE_KEY_ID") != "" &&
		env("APPLE_KEY_SECRET") != "" && ssoRedirectConfigured("APPLE_REDIRECT_URI")
}

func ssoRedirectConfigured(key string) bool {
	return env(key) != "" || env("API_PUBLIC_URL") != ""
}

func checkGoogleSignInIncomplete(ctx context.Context, d Deps, in Input) *Finding {
	if env("GOOGLE_CLIENT_ID") == "" && env("GOOGLE_CLIENT_SECRET") == "" {
		return nil
	}
	if missing := firstUnset("GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET"); missing != "" {
		return result(CategoryAccess, SeverityError, "Sign in with Google is half configured",
			fmt.Sprintf("%s is not set, so the Google button is not shown and the sign-in cannot complete. "+
				"Both halves of the OAuth client are required.", missing),
			docsSignIn)
	}
	if !ssoRedirectConfigured("GOOGLE_REDIRECT_URI") {
		return result(CategoryAccess, SeverityError, "Sign in with Google has no redirect URL",
			"The Google client is configured but there is no redirect URI: API_PUBLIC_URL is empty and GOOGLE_REDIRECT_URI is not set, "+
				"so Sign in with Google is disabled. Set API_PUBLIC_URL to this backend's public base.",
			docsSignIn)
	}
	return misdirectedSSORedirect("Google", "GOOGLE_REDIRECT_URI", "google")
}

func checkAppleSignInIncomplete(ctx context.Context, d Deps, in Input) *Finding {
	keys := []string{"APPLE_APP_ID", "APPLE_TEAM_ID", "APPLE_KEY_ID", "APPLE_KEY_SECRET"}
	if allUnset(keys...) {
		return nil
	}
	if missing := firstUnset(keys...); missing != "" {
		return result(CategoryAccess, SeverityError, "Sign in with Apple is half configured",
			fmt.Sprintf("%s is not set, so the Apple button is not shown. Sign in with Apple needs the Services ID, "+
				"the team id, and the key id and key together.", missing),
			docsSignIn)
	}
	if !ssoRedirectConfigured("APPLE_REDIRECT_URI") {
		return result(CategoryAccess, SeverityError, "Sign in with Apple has no redirect URL",
			"The Apple credentials are set but there is no redirect URI: API_PUBLIC_URL is empty and APPLE_REDIRECT_URI is not set, "+
				"so Sign in with Apple is disabled. Set API_PUBLIC_URL to this backend's public base.",
			docsSignIn)
	}
	if redirect := ssoRedirectURI("APPLE_REDIRECT_URI", "apple"); !strings.HasPrefix(redirect, "https://") {
		return result(CategoryAccess, SeverityError, "Sign in with Apple needs an HTTPS redirect URL",
			fmt.Sprintf("Apple refuses a plain-http return URL, so Sign in with Apple is disabled with the redirect URI at %s. "+
				"Put this backend behind HTTPS.", redirect),
			docsSignIn)
	}
	return misdirectedSSORedirect("Apple", "APPLE_REDIRECT_URI", "apple")
}

// misdirectedSSORedirect catches the mistake that produces a valid OAuth client
// and a broken button: a redirect URI on the dashboard origin. The callback is
// served by the API, so the provider lands the browser on a dashboard route
// that does not exist.
func misdirectedSSORedirect(provider, key, slug string) *Finding {
	raw := env(key)
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || strings.Contains(u.Path, "/v1/auth/") {
		return nil
	}
	if !strings.EqualFold(u.Host, hostOf(appURL())) {
		return nil
	}
	return result(CategoryAccess, SeverityWarning, fmt.Sprintf("The %s redirect URI points at the dashboard", provider),
		fmt.Sprintf("%s is %s, which is the dashboard. The callback is served by the API, so the sign-in ends on a page that does not exist. "+
			"Use %s and register that at the provider.", key, raw, ssoRedirectURI("", slug)),
		docsSignIn)
}

// ssoRedirectURI mirrors the backend's derivation so a finding can name the
// exact value to register at the provider.
func ssoRedirectURI(key, slug string) string {
	if key != "" {
		if v := env(key); v != "" {
			return v
		}
	}
	if base := strings.TrimRight(env("API_PUBLIC_URL"), "/"); base != "" {
		return base + "/v1/auth/" + slug + "/callback"
	}
	return ""
}

func allUnset(keys ...string) bool {
	for _, key := range keys {
		if env(key) != "" {
			return false
		}
	}
	return true
}

func firstUnset(keys ...string) string {
	for _, key := range keys {
		if env(key) == "" {
			return key
		}
	}
	return ""
}

func checkSinglePlatformAdmin(ctx context.Context, d Deps, in Input) *Finding {
	if d.DB == nil {
		return nil
	}
	var count int
	var email string
	err := d.DB.QueryRow(ctx,
		`SELECT count(*), COALESCE(min(email), '') FROM users WHERE (admin_permissions & $1) = $1`,
		int64(models.AdminPermGrantAdminAccess)).Scan(&count, &email)
	if err != nil || count != 1 {
		return nil
	}
	return result(CategoryAccess, SeverityInfo, "Only one admin can grant access",
		fmt.Sprintf("Only one platform admin (%s) can grant admin access. The instance works normally, but if you lose access "+
			"to that account you will need warmblyctl to recover. Add a trusted second admin with Grant admin access from Accounts > Admins.", email),
		docsAdmins)
}

func checkBootstrapPasswordStillSet(ctx context.Context, d Deps, in Input) *Finding {
	if env("WARMBLY_BOOTSTRAP_PASSWORD") == "" || d.DB == nil {
		return nil
	}
	count, err := userCount(ctx, d)
	if err != nil || count == 0 {
		return nil
	}
	return result(CategoryAccess, SeverityWarning, "Bootstrap password is still set",
		"WARMBLY_BOOTSTRAP_PASSWORD is still set in this deployment's environment. "+
			"It is only read while the users table is empty, so it now does nothing except leave a plaintext password "+
			"in your process environment. Remove it.",
		docsFirstOwner)
}

func checkSetupLinkOutstanding(ctx context.Context, d Deps, in Input) *Finding {
	if d.DB == nil || d.Cache == nil {
		return nil
	}
	count, err := userCount(ctx, d)
	if err != nil || count != 0 {
		return nil
	}
	ttl, terr := d.Cache.TTL(ctx, setupTokenKey).Result()
	if terr != nil || ttl <= 0 {
		return nil
	}
	return result(CategoryAccess, SeverityInfo, "A setup link is outstanding",
		fmt.Sprintf("This instance has no accounts yet. A single-use setup link is live for %s; "+
			"`warmblyctl setup-link` prints a fresh one and retires it.", humanizeDuration(ttl)),
		docsFirstOwner)
}

func checkExpiredInvitations(ctx context.Context, d Deps, in Input) *Finding {
	if d.DB == nil {
		return nil
	}
	var count int
	if err := d.DB.QueryRow(ctx,
		`SELECT count(*) FROM organization_invitations WHERE expires_at < NOW()`).Scan(&count); err != nil || count == 0 {
		return nil
	}
	return result(CategoryAccess, SeverityInfo, "Expired invitations",
		fmt.Sprintf("%d expired invitations are stored but can no longer be accepted. They do not block new invitations: "+
			"re-inviting the same address replaces the expired row. You can remove these expired records; active invitations and workspace members are unaffected.", count),
		docsInvitations)
}

func userCount(ctx context.Context, d Deps) (int, error) {
	var count int
	err := d.DB.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&count)
	return count, err
}
