package middleware

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	// authIPWindow and authIPLimit bound unauthenticated auth traffic from one
	// source. The limit is generous enough for a shared office NAT retyping a
	// password, and far below what credential stuffing needs.
	authIPWindow       = 15 * time.Minute
	authIPDefaultLimit = 60

	// The CLI sign-in handshake gets its own budget, on its own key.
	//
	// It cannot share the auth one: `warmbly auth login` polls every
	// CLIAuthPollIntervalSeconds for up to CLIAuthCodeTTLMinutes, which is
	// around 200 requests for a single sign-in. On the shared budget that
	// exhausts the allowance in three minutes, and then blocks the person's
	// actual login from the same address for the rest of the window. The
	// allowance below covers two concurrent sign-ins from one NAT with slack.
	cliAuthIPWindow       = 15 * time.Minute
	cliAuthIPDefaultLimit = 500

	// Minting a passkey login challenge gets its own budget too, and for the
	// same reason.
	//
	// The sign-in page asks for one on load, before anyone has typed anything:
	// one for the passkey button and one for the email field's autofill. On the
	// shared auth budget that is the page spending an office's allowance by
	// being opened, and thirty loads from one NAT lock that address out of
	// password sign-in, registration and password reset for the rest of the
	// window. A challenge is unguessable, single-use and proves nothing, so it
	// does not belong in the budget that bounds password guessing.
	// /passkey/login/finish stays there, because that one is an attempt.
	passkeyIPWindow       = 15 * time.Minute
	passkeyIPDefaultLimit = 300

	// The native app's challenge and key registration are page furniture in the
	// same way, so they get a budget of their own; the request a challenge is
	// spent on stays on /auth.
	appAttestIPWindow       = 15 * time.Minute
	appAttestIPDefaultLimit = 300

	// The remaining public routes (unsubscribe, invitation preview, fleet
	// enrolment, the analytics proxy) carry their authorization in a
	// high-entropy token rather than a session, so this budget is not what
	// stops an attacker reading someone else's data. It stops an unmetered
	// write: every one of those requests reaches Postgres or an upstream, so
	// each has a per-IP ceiling. It is deliberately loose, because
	// a mail provider retrying one-click unsubscribe and an office behind one
	// NAT opening invitations both look like bursts.
	publicIPWindow       = 15 * time.Minute
	publicIPDefaultLimit = 600
)

// PublicIPRateLimitMiddleware bounds the unauthenticated, token-addressed
// routes that sit outside the /auth group.
func (h *Handler) PublicIPRateLimitMiddleware() gin.HandlerFunc {
	// Unlike the auth limiter this counts reads too: the invitation preview and
	// the unsubscribe confirm page both reach Postgres, and the analytics proxy
	// relays a request upstream whatever the method.
	return h.ipRateLimiter("public_ip:", publicIPDefaultLimit, "PUBLIC_IP_RATE_LIMIT", publicIPWindow,
		"Too many requests from this address. Try again shortly.", true)
}

// CLIAuthIPRateLimitMiddleware throttles the public CLI sign-in handshake per
// source IP, on a key of its own so a long poll cannot lock the same address
// out of signing in through the browser.
func (h *Handler) CLIAuthIPRateLimitMiddleware() gin.HandlerFunc {
	return h.ipRateLimiter("cli_auth_ip:", cliAuthIPDefaultLimit, "CLI_AUTH_IP_RATE_LIMIT", cliAuthIPWindow,
		"Too many CLI sign-in requests from this address. Try again later.", false)
}

// PasskeyChallengeIPRateLimitMiddleware throttles the discoverable-login
// challenge per source IP, on a key of its own so a sign-in page that asks for
// one on every load cannot lock the same address out of signing in at all.
func (h *Handler) PasskeyChallengeIPRateLimitMiddleware() gin.HandlerFunc {
	return h.ipRateLimiter("passkey_ip:", passkeyIPDefaultLimit, "PASSKEY_IP_RATE_LIMIT", passkeyIPWindow,
		"Too many passkey sign-in requests from this address. Try again later.", false)
}

// AppAttestIPRateLimitMiddleware throttles the native app's challenge and key
// registration per source IP, on a key of its own.
func (h *Handler) AppAttestIPRateLimitMiddleware() gin.HandlerFunc {
	return h.ipRateLimiter("app_attest_ip:", appAttestIPDefaultLimit, "APP_ATTEST_IP_RATE_LIMIT", appAttestIPWindow,
		"Too many app verification requests from this address. Try again later.", false)
}

// AuthIPRateLimitMiddleware throttles the public /auth group per source IP,
// bounding password guessing and the 64 MiB Argon2 hash each attempt costs.
//
// Fails open on a cache error, deliberately: a Redis blip must not lock every
// user out of their own instance.
func (h *Handler) AuthIPRateLimitMiddleware() gin.HandlerFunc {
	return h.ipRateLimiter("auth_ip:", authIPDefaultLimit, "AUTH_IP_RATE_LIMIT", authIPWindow,
		"Too many authentication attempts from this address. Try again later.", false)
}

// ipRateLimiter is the shared fixed-window limiter behind both. Each caller
// brings its own Redis key prefix, so budgets never bleed into each other.
func (h *Handler) ipRateLimiter(prefix string, defaultLimit int, env string, window time.Duration, message string, countReads bool) gin.HandlerFunc {
	limit := defaultLimit
	if v := os.Getenv(env); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	return func(c *gin.Context) {
		if h.Cache == nil {
			c.Next()
			return
		}
		// Reads are cheap and the login screen makes one on every load, so the
		// auth limiter lets them past. A limiter whose routes read the database
		// on GET asks for them to be counted.
		if !countReads && c.Request.Method == http.MethodGet {
			c.Next()
			return
		}

		ip := c.ClientIP()
		if ip == "" {
			c.Next()
			return
		}

		key := prefix + ip
		n, err := h.Cache.Incr(c.Request.Context(), key).Result()
		if err != nil {
			c.Next()
			return
		}
		// A counter with no TTL never resets, so the address it belongs to
		// stays blocked forever once it passes the limit. That is a worse
		// outcome than not counting at all, so a failed EXPIRE drops the key
		// and lets the request through, matching how the rest of this
		// middleware handles a cache it cannot trust.
		if n == 1 {
			if err := h.Cache.Expire(c.Request.Context(), key, window).Err(); err != nil {
				_ = h.Cache.Del(c.Request.Context(), key).Err()
				c.Next()
				return
			}
		} else if n > int64(limit) {
			// Repair a key that lost its expiry some other way (an older
			// build, a restore, an eviction between the INCR and the EXPIRE
			// above). Only on the reject path, which is rare, so it costs a
			// round trip nobody feels.
			if ttl, terr := h.Cache.TTL(c.Request.Context(), key).Result(); terr == nil && ttl < 0 {
				_ = h.Cache.Expire(c.Request.Context(), key, window).Err()
			}
		}

		if n > int64(limit) {
			c.Header("Retry-After", fmt.Sprintf("%d", int(window.Seconds())))
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":   "rate_limit_exceeded",
				"message": message,
				"code":    "rate_limit_exceeded",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}
