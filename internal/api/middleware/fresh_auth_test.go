package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/warmbly/warmbly/internal/app/token"
	"github.com/warmbly/warmbly/internal/models"
)

// A session caller reaches a fresh-auth route only within the reauth window; a
// credential that has no session passes to the route's own permission gate.
func TestRequireFreshAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recent := time.Now().Add(-time.Minute)
	stale := time.Now().Add(-token.ReauthWindow - time.Second)
	cases := []struct {
		name     string
		authType string
		session  *models.Session
		want     int
	}{
		{"session confirmed a minute ago", AuthTypeJWT, &models.Session{ReauthAt: &recent}, http.StatusNoContent},
		{"session confirmed past the window", AuthTypeJWT, &models.Session{ReauthAt: &stale}, http.StatusForbidden},
		{"session that never confirmed", AuthTypeJWT, &models.Session{}, http.StatusForbidden},
		{"no session at all", AuthTypeJWT, nil, http.StatusUnauthorized},
		{"api key", AuthTypeAPIKey, nil, http.StatusNoContent},
		{"oauth token", AuthTypeOAuth, nil, http.StatusNoContent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.POST("/x", func(c *gin.Context) {
				c.Set(AuthTypeKey, tc.authType)
				if tc.session != nil {
					c.Set(SessionKey, tc.session)
				}
			}, RequireFreshAuth(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/x", nil))
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.want, w.Body)
			}
		})
	}
}
