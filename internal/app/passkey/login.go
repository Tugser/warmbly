package passkey

import (
	"context"
	"errors"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/app/token"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/observability/errs"
)

func (s *service) BeginLogin(ctx context.Context) (*LoginChallenge, *errx.Error) {
	options, sessionData, err := s.web.BeginDiscoverableLogin(
		webauthn.WithUserVerification(protocol.VerificationRequired),
	)
	if err != nil {
		return nil, errx.ErrPasskey
	}

	sessionID := uuid.New()
	if xerr := s.saveSession(ctx, loginKey(sessionID), sessionData); xerr != nil {
		return nil, xerr
	}

	return &LoginChallenge{
		Session: sessionID.String(),
		Options: options,
	}, nil
}

func (s *service) FinishLogin(ctx context.Context, session string, credential []byte, ipaddr, userAgent string) (*models.Token, *errx.Error) {
	sessionID, err := uuid.Parse(session)
	if err != nil {
		return nil, errx.ErrPasskeySession
	}

	sessionData, xerr := s.takeSession(ctx, loginKey(sessionID))
	if xerr != nil {
		return nil, xerr
	}

	parsed, perr := protocol.ParseCredentialRequestResponseBytes(credential)
	if perr != nil {
		return nil, errx.ErrPasskey
	}

	// The session counts as multi-factor, which only a user-verified assertion
	// earns. The flag is inside the signed authenticator data validated below.
	if !parsed.Response.AuthenticatorData.Flags.HasUserVerified() {
		return nil, errx.ErrPasskeyUserVerification
	}

	// The discoverable assertion carries the user handle; resolve the account
	// from it. ValidatePasskeyLogin then verifies the signature against that
	// account's stored credential.
	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		return s.userByHandle(ctx, userHandle)
	}

	userIface, wcred, verr := s.web.ValidatePasskeyLogin(handler, *sessionData, parsed)
	if verr != nil {
		return nil, errx.ErrPasskey
	}

	resolved, ok := userIface.(*webAuthnUser)
	if !ok || resolved == nil {
		return nil, errx.ErrPasskey
	}

	// Persist the post-assertion counter, clone flag, backup state, and
	// last-used time so clone detection and replay protection hold across
	// logins. Best-effort: a write failure must not block a valid sign-in.
	_ = s.repo.TouchCredential(ctx, wcred.ID, wcred.Authenticator.SignCount, wcred.Authenticator.CloneWarning, wcred.Flags.BackupState, time.Now())

	user := resolved.user

	// Ban-scope enforcement — parity with password login (auth/login.go).
	// A ban state that cannot be read refuses the sign-in.
	scope, scopeErr := s.userRepo.GetBanState(ctx, user.ID)
	if scopeErr != nil {
		errs.CaptureException(scopeErr)
		return nil, errx.InternalError()
	}
	if models.BanScope(scope).Has(models.BanScopeLogin) {
		return nil, errx.ErrAccountSuspended
	}

	// No TOTP step: a user-verified passkey is possession plus a PIN or
	// biometric, so it is multi-factor on its own and the session is verified.
	tok, xerr := s.token.GenerateMFASession(ctx, user.ID, user.Email, ipaddr, userAgent, token.AuthProviderWebAuthn)
	if xerr != nil {
		return nil, xerr
	}

	return tok, nil
}

// userByHandle resolves the discoverable assertion's user handle (the account
// UUID bytes) back to the engine user.
func (s *service) userByHandle(ctx context.Context, userHandle []byte) (webauthn.User, error) {
	uid, err := uuid.FromBytes(userHandle)
	if err != nil {
		return nil, err
	}

	u, uerr := s.userRepo.GetUser(ctx, uid)
	if uerr != nil || u == nil {
		return nil, errors.New("passkey: user not found")
	}

	creds, cerr := s.repo.ListByUser(ctx, uid)
	if cerr != nil {
		return nil, errors.New("passkey: failed to load credentials")
	}

	return newWebAuthnUser(u, creds), nil
}
