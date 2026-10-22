package config

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/warmbly/warmbly/internal/pkg/secretset"
)

type AuthConfig struct {
	// Browser social sign-in. The redirect URIs are where the provider sends
	// the browser back, which is the API, not the dashboard: both default to
	// API_PUBLIC_URL plus /v1/auth/<provider>/callback when left unset.
	GoogleClientID     string
	GoogleRedirectURI  string
	GoogleClientSecret string

	// AppleAppID is the Services ID (the web identifier), not the app's
	// bundle ID.
	AppleAppID       string
	AppleTeamID      string
	AppleKeyID       string
	AppleKeySecret   string
	AppleRedirectURI string

	// Native-app (iOS) social sign-in. ID-token verification only needs the
	// expected audiences: the app's bundle ID for Sign in with Apple (fixed by
	// the shipped binary) and the iOS OAuth client ID for Google (empty
	// disables the Google option and hides its button in the app).
	AppleIOSBundleID  string
	GoogleIOSClientID string

	AuthSecret               string
	UnsubscribeSigningSecret string
	UnsubscribeReadSecrets   []string
	UnsubscribeReadUntil     time.Time
	DomainProofSecret        string
	TurnstileSecret          string
	TurnstileBypass          string

	// AppAttestAppIDs are the "<team id>.<bundle id>" values whose App Attest
	// proofs stand in for the captcha. Empty turns native app proofs off.
	AppAttestAppIDs []string
	// AppAttestAllowDevelopment accepts proofs from development-signed builds.
	AppAttestAllowDevelopment bool

	// TwoFASecret is the server-wide key the TOTP secret is sealed with (AES-GCM).
	// Must be set when 2FA is in use; falls back to AuthSecret when unset so a
	// deployment without the env var still functions (sealed under the auth key).
	TwoFASecret      string
	TwoFAReadSecrets []string

	// WebAuthn / passkey relying-party configuration.
	//
	// WebAuthnRPID is the relying-party ID a passkey is cryptographically
	// bound to — the registrable domain the dashboard is served from
	// (e.g. "app.warmbly.com"), with no scheme or port. WebAuthnRPOrigins
	// are the full scheme-qualified origins allowed to run ceremonies
	// (e.g. "https://app.warmbly.com"). Both are derived from APP_URL /
	// CORS_ALLOW_ORIGINS so self-hosted and local-dev deployments work out
	// of the box, and can be overridden explicitly with WEBAUTHN_RP_ID /
	// WEBAUTHN_RP_ORIGINS. Changing the RP ID invalidates every enrolled
	// passkey, so it must stay stable for a given deployment.
	WebAuthnRPID          string
	WebAuthnRPDisplayName string
	WebAuthnRPOrigins     []string
}

// MinAuthSecretLength is the floor for AUTH_SECRET. HS256 keys shorter than
// the 256-bit hash output are brute-forceable offline from any token the
// service has ever issued.
const MinAuthSecretLength = 32

func (c *Config) LoadAuthConfig(ctx context.Context) (*AuthConfig, error) {
	// Social sign-in (Google/Apple) and captcha (Turnstile) are optional: a
	// self-hosted install running only email+password / passkeys needs none of
	// them, so leaving these unset disables the corresponding provider rather
	// than failing boot. AUTH_SECRET (JWT/session signing) stays required.
	googleClientID := c.GetStringOptional(ctx, "GOOGLE_CLIENT_ID", "google-auth/client_id", "")
	googleRedirectURI := c.GetStringOptional(ctx, "GOOGLE_REDIRECT_URI", "google-auth/redirect_uri", "")
	googleClientSecret := c.GetSecretOptional(ctx, "GOOGLE_CLIENT_SECRET", "google-auth/client_secret", "")

	appleAppID := c.GetStringOptional(ctx, "APPLE_APP_ID", "apple-auth/app_id", "")
	appleRedirectURI := c.GetStringOptional(ctx, "APPLE_REDIRECT_URI", "apple-auth/redirect_uri", "")
	appleTeamID := c.GetStringOptional(ctx, "APPLE_TEAM_ID", "apple-auth/team_id", "")
	appleKeyID := c.GetStringOptional(ctx, "APPLE_KEY_ID", "apple-auth/key_id", "")
	appleKeySecret := c.GetSecretOptional(ctx, "APPLE_KEY_SECRET", "apple-auth/key_secret", "")

	appleIOSBundleID := c.GetStringOptional(ctx, "APPLE_IOS_BUNDLE_ID", "apple-auth/ios_bundle_id", "com.warmbly.app")
	googleIOSClientID := c.GetStringOptional(ctx, "GOOGLE_IOS_CLIENT_ID", "google-auth/ios_client_id", "")

	authSecret, err := c.GetSecret(ctx, "AUTH_SECRET", "auth_secret")
	if err != nil {
		return nil, err
	}
	// HS256 signs every session, refresh, websocket, reset and challenge token
	// with this one value, and RFC 7518 section 3.2 requires a key at least as
	// long as the hash output. Presence was the only check, so a deployment
	// could sign its whole auth system with a ten-character string. The
	// realtime service applies the same floor to JWT_SECRET, which is this
	// same value.
	if len(authSecret) < MinAuthSecretLength {
		return nil, fmt.Errorf(
			"AUTH_SECRET must be at least %d characters (it signs every session token); generate one with: make gen-key",
			MinAuthSecretLength)
	}

	turnstileSecret := c.GetSecretOptional(ctx, "TURNSTILE_SECRET", "turnstile/secret", "")
	SetTurnstileSecretResolved(turnstileSecret != "")
	turnstileBypass := c.GetSecretOptional(ctx, "TURNSTILE_BYPASS_TOKEN", "turnstile/bypass_token", "")

	// Defaults to the iOS app this instance's Apple team ships, when it names one.
	appAttestAppIDs := splitCSV(c.GetStringOptional(ctx, "APP_ATTEST_APP_IDS", "app-attest/app_ids", ""))
	if len(appAttestAppIDs) == 0 && appleTeamID != "" && appleIOSBundleID != "" {
		appAttestAppIDs = []string{appleTeamID + "." + appleIOSBundleID}
	}
	appAttestAllowDev := c.GetBoolOptional(ctx, "APP_ATTEST_ALLOW_DEVELOPMENT", "app-attest/allow_development", false)

	// 2FA sealing key. Optional: falls back to AUTH_SECRET so existing
	// deployments keep working; rotating it invalidates enrolled TOTP secrets.
	twoFASecret := c.GetSecretOptional(ctx, "TWOFA_SECRET", "twofa_secret", authSecret)
	twoFAReadSecrets, err := secretset.Parse(c.GetSecretOptional(ctx, "TWOFA_READ_SECRETS", "twofa_read_secrets", ""))
	if err != nil {
		return nil, fmt.Errorf("TWOFA_READ_SECRETS: %w", err)
	}
	unsubscribeSigningSecret := c.GetSecretOptional(ctx, "UNSUBSCRIBE_SIGNING_SECRET", "unsubscribe/signing_secret", authSecret)
	domainProofSecret := c.GetSecretOptional(ctx, "DOMAIN_PROOF_SECRET", "domain_proof_secret", authSecret)
	if len(unsubscribeSigningSecret) < MinAuthSecretLength || len(domainProofSecret) < MinAuthSecretLength {
		return nil, fmt.Errorf("unsubscribe and domain proof signing secrets require at least %d characters", MinAuthSecretLength)
	}
	unsubscribeReadSecrets, err := secretset.Parse(c.GetSecretOptional(ctx, "UNSUBSCRIBE_SIGNING_READ_SECRETS", "unsubscribe/signing_read_secrets", ""))
	if err != nil {
		return nil, fmt.Errorf("UNSUBSCRIBE_SIGNING_READ_SECRETS: %w", err)
	}
	var unsubscribeReadUntil time.Time
	if len(unsubscribeReadSecrets) > 0 {
		unsubscribeReadUntil, err = time.Parse(time.RFC3339, c.GetStringOptional(ctx, "UNSUBSCRIBE_SIGNING_READ_UNTIL", "unsubscribe/signing_read_until", ""))
		if err != nil {
			return nil, fmt.Errorf("UNSUBSCRIBE_SIGNING_READ_SECRETS requires an RFC3339 UNSUBSCRIBE_SIGNING_READ_UNTIL")
		}
	}

	rpDisplayName := c.GetStringOptional(ctx, "WEBAUTHN_RP_DISPLAY_NAME", "webauthn/rp_display_name", "Warmbly")
	rpIDRaw := c.GetStringOptional(ctx, "WEBAUTHN_RP_ID", "webauthn/rp_id", "")
	rpOriginsRaw := c.GetStringOptional(ctx, "WEBAUTHN_RP_ORIGINS", "webauthn/rp_origins", "")
	rpID, rpOrigins := resolveWebAuthnRP(rpIDRaw, rpOriginsRaw)

	return &AuthConfig{
		GoogleClientID:     googleClientID,
		GoogleClientSecret: googleClientSecret,
		GoogleRedirectURI:  googleRedirectURI,

		AppleAppID:       appleAppID,
		AppleTeamID:      appleTeamID,
		AppleKeyID:       appleKeyID,
		AppleKeySecret:   appleKeySecret,
		AppleRedirectURI: appleRedirectURI,

		AppleIOSBundleID:  appleIOSBundleID,
		GoogleIOSClientID: googleIOSClientID,

		AuthSecret:               authSecret,
		UnsubscribeSigningSecret: unsubscribeSigningSecret,
		UnsubscribeReadSecrets:   unsubscribeReadSecrets,
		UnsubscribeReadUntil:     unsubscribeReadUntil,
		DomainProofSecret:        domainProofSecret,
		TurnstileSecret:          turnstileSecret,
		TurnstileBypass:          turnstileBypass,

		AppAttestAppIDs:           appAttestAppIDs,
		AppAttestAllowDevelopment: appAttestAllowDev,
		TwoFASecret:               twoFASecret,
		TwoFAReadSecrets:          twoFAReadSecrets,

		WebAuthnRPID:          rpID,
		WebAuthnRPDisplayName: rpDisplayName,
		WebAuthnRPOrigins:     rpOrigins,
	}, nil
}

// resolveWebAuthnRP derives the passkey relying-party ID and allowed origins.
//
// Origins come from (in order): an explicit WEBAUTHN_RP_ORIGINS list, then
// CORS_ALLOW_ORIGINS, then APP_URL, then a localhost dev fallback. The RP ID
// is an explicit override if given, otherwise the host of the first origin
// (scheme and port stripped), otherwise "localhost". This keeps prod, self
// hosted, and local-dev deployments correct without per-environment code.
func resolveWebAuthnRP(explicitRPID, explicitOrigins string) (string, []string) {
	originsRaw := explicitOrigins
	if originsRaw == "" {
		originsRaw = os.Getenv("CORS_ALLOW_ORIGINS")
	}
	if originsRaw == "" {
		originsRaw = os.Getenv("APP_URL")
	}

	origins := splitCSV(originsRaw)
	if len(origins) == 0 {
		origins = []string{"http://localhost:5173"}
	}

	rpID := strings.TrimSpace(explicitRPID)
	if rpID == "" {
		rpID = hostFromOrigin(origins[0])
	}
	if rpID == "" {
		rpID = "localhost"
	}

	return rpID, origins
}

// hostFromOrigin returns the bare hostname of an origin (no scheme, no port),
// suitable for use as a WebAuthn RP ID. Returns "" if the origin can't be
// parsed into a host (e.g. a wildcard "*").
func hostFromOrigin(origin string) string {
	u, err := url.Parse(strings.TrimSpace(origin))
	if err != nil || u.Hostname() == "" {
		return ""
	}
	return u.Hostname()
}
