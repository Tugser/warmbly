package auth

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/observability/errs"
	"github.com/warmbly/warmbly/internal/pkg/crypt"
)

// Login and registration keep separate send budgets, so registering an address cannot spend its login budget.
func getEmailVerificationKey(flow, email string) string {
	return "email_verification:" + flow + ":" + crypt.SHA256(email)
}

func getPasswordResetLimitKey(email string) string {
	return "password_reset_limit:" + crypt.SHA256(email)
}

// getLoginFailureKey counts wrong passwords for one address. Keyed on the
// address rather than the user id because the lookup that would resolve the id
// is the thing being throttled, and a miss must cost the guesser the same as a
// hit.
func getLoginFailureKey(email string) string {
	return "login_fail:" + crypt.SHA256(email)
}

// getReauthFailureKey counts failed confirmations for one account. The
// re-authentication endpoint checks a password, so without its own budget it is
// a second, unthrottled place to guess one: the per-IP limiter allows a few
// hundred an hour and the per-account login counter does not see this path.
func getReauthFailureKey(userID uuid.UUID) string {
	return "reauth_fail:" + userID.String()
}

func getLoginSessionKey(sessionID uuid.UUID) string {
	return "login_sess:" + sessionID.String()
}

func getRegistrationSessionKey(sessionID uuid.UUID) string {
	return "registration_sess:" + sessionID.String()
}

func getResetPasswordSessionKey(sessionID uuid.UUID) string {
	return "reset_password:" + sessionID.String()
}

func (s *authService) saveLoginSession(ctx context.Context, sessionID uuid.UUID, session *models.LoginSession, expiresAt time.Time) *errx.Error {
	data, err := json.Marshal(session)
	if err != nil {
		errs.CaptureException(err)
		return errx.InternalError()
	}

	if err := s.cache.Set(ctx, getLoginSessionKey(sessionID), data, time.Until(expiresAt)).Err(); err != nil {
		errs.CaptureException(err)
		return errx.InternalError()
	}

	return nil
}

func (s *authService) getLoginSession(ctx context.Context, sessionID uuid.UUID) (*models.LoginSession, *errx.Error) {
	data, err := s.cache.Get(ctx, getLoginSessionKey(sessionID)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		errs.CaptureException(err)
		return nil, errx.InternalError()
	}

	var session models.LoginSession
	if err := json.Unmarshal(data, &session); err != nil {
		errs.CaptureException(err)
		return nil, errx.InternalError()
	}

	return &session, nil
}

func (s *authService) saveRegistrationSession(ctx context.Context, sessionID uuid.UUID, session *models.RegistrationSession, expiresAt time.Time) *errx.Error {
	data, err := json.Marshal(session)
	if err != nil {
		errs.CaptureException(err)
		return errx.InternalError()
	}

	if err := s.cache.Set(ctx, getRegistrationSessionKey(sessionID), data, time.Until(expiresAt)).Err(); err != nil {
		errs.CaptureException(err)
		return errx.InternalError()
	}

	return nil
}

func (s *authService) getRegistrationSession(ctx context.Context, sessionID uuid.UUID) (*models.RegistrationSession, *errx.Error) {
	data, err := s.cache.Get(ctx, getRegistrationSessionKey(sessionID)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		errs.CaptureException(err)
		return nil, errx.InternalError()
	}

	var session models.RegistrationSession
	if err := json.Unmarshal(data, &session); err != nil {
		errs.CaptureException(err)
		return nil, errx.InternalError()
	}

	return &session, nil
}

func (s *authService) canSendEmail(ctx context.Context, flow, email string) *errx.Error {
	key := getEmailVerificationKey(flow, email)

	count, err := s.cache.Incr(ctx, key).Result()
	if err != nil {
		errs.CaptureException(err)
		return errx.InternalError()
	}

	if count == 1 {
		if err := s.cache.Expire(ctx, key, AuthEmailTTL).Err(); err != nil {
			errs.CaptureException(err)
			return errx.InternalError()
		}
	}

	if count > AuthEmailLimit {
		return errx.ErrAuthLimit
	}

	return nil
}

func (s *authService) passwordResetLimit(ctx context.Context, email string) *errx.Error {
	key := getPasswordResetLimitKey(email)

	count, err := s.cache.Incr(ctx, key).Result()
	if err != nil {
		errs.CaptureException(err)
		return errx.InternalError()
	}

	if count == 1 {
		if err := s.cache.Expire(ctx, key, PasswordResetLimitTTL).Err(); err != nil {
			errs.CaptureException(err)
			return errx.InternalError()
		}
	}

	if count > PasswordResetLimit {
		return errx.ErrAuthLimit
	}

	return nil
}

// refundPasswordResetLimit gives back an attempt that produced no mail through
// no fault of the person asking. The budget is only two requests per four
// hours, so without this a transient SES rejection or a cache blip spent half
// of someone's allowance and a second one locked them out of the flow for the
// rest of the afternoon, with nothing in their inbox to explain why. Best
// effort: failing to refund must never turn into a failed request on top of
// the one that already failed.
//
// Only OUR failures are refunded. An address with no account still pays, or
// the counter stops costing an attacker anything to probe with.
func (s *authService) refundPasswordResetLimit(ctx context.Context, email string) {
	key := getPasswordResetLimitKey(email)

	// DECR cannot take the counter below what this request added: the key is
	// only ever incremented by a request that reaches here to undo it, and a
	// key that expired in between comes back at -1 with no TTL, which would
	// hand out unlimited attempts. Refuse that case rather than create it.
	count, err := s.cache.Decr(ctx, key).Result()
	if err != nil {
		errs.CaptureException(err)
		return
	}
	if count < 0 {
		if err := s.cache.Del(ctx, key).Err(); err != nil {
			errs.CaptureException(err)
		}
	}
}

// saveResetPasswordSession binds the emailed reset JWT to a server-side nonce.
// The TTL is PasswordResetTTL, the same lifetime the JWT carries and the email quotes.
func (s *authService) saveResetPasswordSession(ctx context.Context, sessionID uuid.UUID, nonce string) *errx.Error {
	if err := s.cache.SetEx(ctx, getResetPasswordSessionKey(sessionID), nonce, PasswordResetTTL).Err(); err != nil {
		errs.CaptureException(err)
		return errx.InternalError()
	}

	return nil
}

func (s *authService) getResetPasswordSession(ctx context.Context, sessionID uuid.UUID) (string, *errx.Error) {
	val, err := s.cache.Get(ctx, getResetPasswordSessionKey(sessionID)).Result()
	if err != nil {
		// An expired or already-used link is ordinary, not an internal fault.
		if errors.Is(err, redis.Nil) {
			return "", errx.ErrToken
		}
		errs.CaptureException(err)
		return "", errx.InternalError()
	}

	return val, nil
}

// getResetHandleKey stores a reset session under its hash, so the cache never holds a usable handle.
func getResetHandleKey(handle string) string {
	return "reset_password_handle:" + crypt.SHA256(handle)
}

// saveResetHandle maps an opaque reset session to the link token it was opened with.
func (s *authService) saveResetHandle(ctx context.Context, handle, linkToken string, ttl time.Duration) *errx.Error {
	if err := s.cache.SetEx(ctx, getResetHandleKey(handle), linkToken, ttl).Err(); err != nil {
		errs.CaptureException(err)
		return errx.InternalError()
	}
	return nil
}

func (s *authService) getResetHandle(ctx context.Context, handle string) (string, *errx.Error) {
	val, err := s.cache.Get(ctx, getResetHandleKey(handle)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", errx.ErrToken
		}
		errs.CaptureException(err)
		return "", errx.InternalError()
	}
	return val, nil
}

func (s *authService) deleteResetHandle(ctx context.Context, handle string) {
	if err := s.cache.Del(ctx, getResetHandleKey(handle)).Err(); err != nil {
		errs.CaptureException(err)
	}
}

func (s *authService) deletePasswordResetSession(ctx context.Context, sessionID uuid.UUID) *errx.Error {
	val, err := s.cache.Del(ctx, getResetPasswordSessionKey(sessionID)).Result()
	if err != nil {
		errs.CaptureException(err)
		return errx.InternalError()
	}

	if val == 0 {
		return errx.ErrToken
	}

	return nil
}

// reserveAttempt charges one attempt before a credential is compared, so
// concurrent guesses cannot all pass a read-only check. A cache error refuses
// with a retryable 503: no credential is compared without a charged attempt.
func (s *authService) reserveAttempt(ctx context.Context, key string, limit int64, window time.Duration) (bool, *errx.Error) {
	ok, err := s.cache.ReserveAttempt(ctx, key, limit, window)
	if err != nil {
		errs.CaptureException(err)
		return false, errx.ErrAttemptCheckUnavailable
	}
	return ok, nil
}

// releaseAttempt refunds a reserved attempt that ended before any credential
// was compared.
func (s *authService) releaseAttempt(ctx context.Context, key string) {
	if err := s.cache.ReleaseAttempt(ctx, key); err != nil {
		errs.CaptureException(err)
	}
}

// reserveLoginAttempt charges one password attempt to the address.
func (s *authService) reserveLoginAttempt(ctx context.Context, email string) (bool, *errx.Error) {
	return s.reserveAttempt(ctx, getLoginFailureKey(email), LoginFailureLimit, LoginFailureTTL)
}

// releaseLoginAttempt refunds a password attempt that compared nothing.
func (s *authService) releaseLoginAttempt(ctx context.Context, email string) {
	s.releaseAttempt(ctx, getLoginFailureKey(email))
}

// reserveCodeAttempt charges one try to an emailed-code session before the
// code is compared; the counter lives no longer than the session.
func (s *authService) reserveCodeAttempt(ctx context.Context, sessionKey string, ttl time.Duration) (bool, *errx.Error) {
	return s.reserveAttempt(ctx, sessionKey+":tries", AuthAttempts, ttl)
}

// consumeCodeSession deletes an emailed-code session and reports whether this
// call was the one that removed it, so a code is accepted exactly once.
func (s *authService) consumeCodeSession(ctx context.Context, sessionKey string) bool {
	n, err := s.cache.Del(ctx, sessionKey).Result()
	if err != nil {
		errs.CaptureException(err)
		return false
	}
	_ = s.cache.Del(ctx, sessionKey+":tries").Err()
	return n == 1
}

// clearLoginFailures forgives the count once the right password arrives, so a
// person who mistypes a few times and then gets it right starts clean.
func (s *authService) clearLoginFailures(ctx context.Context, email string) {
	if err := s.cache.Del(ctx, getLoginFailureKey(email)).Err(); err != nil {
		errs.CaptureException(err)
	}
}

// ReserveReauthAttempt charges one attempt to the account before the proof is
// checked. The signed-in password change spends the same budget.
func (s *authService) ReserveReauthAttempt(ctx context.Context, userID uuid.UUID) (bool, *errx.Error) {
	return s.reserveAttempt(ctx, getReauthFailureKey(userID), LoginFailureLimit, LoginFailureTTL)
}

// ReleaseReauthAttempt refunds a reserved attempt that ended before any
// credential was compared.
func (s *authService) ReleaseReauthAttempt(ctx context.Context, userID uuid.UUID) {
	s.releaseAttempt(ctx, getReauthFailureKey(userID))
}

// ClearReauthFailures forgives the count once a confirmation succeeds.
func (s *authService) ClearReauthFailures(ctx context.Context, userID uuid.UUID) {
	if err := s.cache.Del(ctx, getReauthFailureKey(userID)).Err(); err != nil {
		errs.CaptureException(err)
	}
}
