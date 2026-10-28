package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/warmbly/warmbly/internal/app/token"
	"github.com/warmbly/warmbly/internal/errx"
)

// SessionFresh reports whether the caller's session re-proved the account holder within the reauth window.
func SessionFresh(c *gin.Context) bool {
	session := GetSession(c)
	return session != nil && session.ReauthAt != nil && time.Since(*session.ReauthAt) <= token.ReauthWindow
}

// RequireFreshAuth refuses a session that has not re-proved the account holder within token.ReauthWindow.
// API-key and OAuth callers pass: they present a scoped credential on every call and have no session to refresh.
func RequireFreshAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if authType, _ := c.Get(AuthTypeKey); authType == AuthTypeAPIKey || authType == AuthTypeOAuth {
			c.Next()
			return
		}

		session := GetSession(c)
		if session == nil {
			errx.JSON(c, errx.NewWithIdentifier(errx.Unauthorized, "reauth_required",
				"This action needs a signed-in session. Sign in and try again."))
			c.Abort()
			return
		}
		if session.ReauthAt == nil || time.Since(*session.ReauthAt) > token.ReauthWindow {
			errx.JSON(c, errx.NewWithIdentifier(errx.Forbidden, "reauth_required",
				"Confirm it is you before making this change."))
			c.Abort()
			return
		}
		c.Next()
	}
}
