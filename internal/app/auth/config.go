package auth

import "time"

const (
	SessionTTL   = 10 * time.Minute
	AuthAttempts = 3

	// LoginFailureLimit and LoginFailureTTL bound password guessing against one
	// account, which the per-IP limiter cannot: a guesser with a botnet spends a
	// fresh 60-request budget per source address while the account it is aimed
	// at counts nothing.
	//
	// ASVS 2.2.1 requires fewer than 100 failures per hour, so this sits well
	// under that line. It stays far above ten because a counter keyed on an
	// address is a lockout anyone can trigger at somebody else's account.
	// A correct password and a completed reset both clear it.
	LoginFailureLimit = 50
	LoginFailureTTL   = 1 * time.Hour

	AuthSessionTTL   = 10 * time.Minute
	AuthEmailTTL     = 30 * time.Minute
	AuthEmailLimit   = 5
	PasswordResetTTL = 1 * time.Hour
	// PasswordResetSessionTTL bounds the reset session a link opens, never past the link itself.
	PasswordResetSessionTTL = 15 * time.Minute

	PasswordResetLimit    = 2
	PasswordResetLimitTTL = 4 * time.Hour

	// TrustedDeviceTTL is how long a device the user asked to remember stays
	// exempt from the login code under AUTH_LOGIN_CODE=new_device. Absolute:
	// signing in again does not extend it.
	TrustedDeviceTTL = 30 * 24 * time.Hour
)

// Email send-budget flows. Separate keys so registration traffic cannot exhaust
// a user's login budget.
const (
	emailFlowLogin        = "login"
	emailFlowRegistration = "registration"
)
