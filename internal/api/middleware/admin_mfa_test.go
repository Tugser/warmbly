package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/organization"
	"github.com/warmbly/warmbly/internal/models"
)

type adminPermOrgs struct {
	organization.OrganizationService
	perms uint32
}

func (f adminPermOrgs) GetUserAdminPermissions(context.Context, uuid.UUID) (uint32, error) {
	return f.perms, nil
}

// An admin route answers only a session that verified a second factor, and then only with the right admin bit.
func TestAdminRoutesRequireSecondFactorAndPermission(t *testing.T) {
	for _, tc := range []struct {
		name    string
		session *models.Session
		perms   models.AdminPermission
		code    int
		ident   string
	}{
		{"no session is refused", nil, models.AdminPermViewUsers, http.StatusUnauthorized, ""},
		{"a non-admin is refused", &models.Session{UserID: uuid.New(), MFAVerified: true}, 0, http.StatusForbidden, ""},
		{"an admin without a second factor is refused", &models.Session{UserID: uuid.New()}, models.AdminPermViewUsers, http.StatusForbidden, "admin_mfa_required"},
		{"an admin with a second factor but not the route's bit is refused", &models.Session{UserID: uuid.New(), MFAVerified: true}, models.AdminPermBanUsers, http.StatusForbidden, ""},
		{"an admin with a second factor and the route's bit passes", &models.Session{UserID: uuid.New(), MFAVerified: true}, models.AdminPermViewUsers, http.StatusOK, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{OrganizationService: adminPermOrgs{perms: uint32(tc.perms)}}
			r := gin.New()
			r.Use(RequestIDMiddleware())
			r.GET("/admin/users", func(c *gin.Context) {
				if tc.session != nil {
					c.Set(SessionKey, tc.session)
				}
				c.Next()
			}, h.AdminMiddleware(), RequireAdminPermission(models.AdminPermViewUsers), func(c *gin.Context) { c.Status(http.StatusOK) })
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/admin/users", nil))
			if w.Code != tc.code {
				t.Fatalf("status = %d, want %d; body=%s", w.Code, tc.code, w.Body.String())
			}
			if tc.ident != "" && !strings.Contains(w.Body.String(), `"`+tc.ident+`"`) {
				t.Fatalf("body = %s, want code %s", w.Body.String(), tc.ident)
			}
		})
	}
}
