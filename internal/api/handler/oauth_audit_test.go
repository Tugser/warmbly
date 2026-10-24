package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/api/middleware"
	"github.com/warmbly/warmbly/internal/app/audit"
	"github.com/warmbly/warmbly/internal/app/oauth"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// fakeAppRepo holds one application; deleteErr makes the delete fail.
type fakeAppRepo struct {
	repository.OAuthRepository
	app       *models.OAuthApplication
	deleteErr error
}

func (f *fakeAppRepo) DeveloperBlock(context.Context, uuid.UUID, uuid.UUID) (*models.OAuthDeveloperBlock, error) {
	return nil, nil
}

func (f *fakeAppRepo) CreateApplication(_ context.Context, a *models.OAuthApplication) error {
	a.ID = uuid.New()
	f.app = a
	return nil
}

func (f *fakeAppRepo) GetApplication(_ context.Context, _, id uuid.UUID) (*models.OAuthApplication, error) {
	if f.app == nil || f.app.ID != id {
		return nil, nil
	}
	copied := *f.app
	return &copied, nil
}

func (f *fakeAppRepo) UpdateApplication(_ context.Context, a *models.OAuthApplication) error {
	f.app = a
	return nil
}

func (f *fakeAppRepo) UpdateApplicationSecret(context.Context, uuid.UUID, uuid.UUID, string) error {
	return nil
}

func (f *fakeAppRepo) DeleteApplication(context.Context, uuid.UUID, uuid.UUID) error {
	return f.deleteErr
}

type auditEntry struct {
	action   models.AuditAction
	entity   models.AuditEntityType
	id       uuid.UUID
	metadata map[string]string
}

type recordingAudit struct {
	audit.AuditService
	entries []auditEntry
}

func (r *recordingAudit) LogAction(_ context.Context, _, _ uuid.UUID, action models.AuditAction, entity models.AuditEntityType, id *uuid.UUID, _, _ string, _, metadata map[string]string) {
	e := auditEntry{action: action, entity: entity, metadata: metadata}
	if id != nil {
		e.id = *id
	}
	r.entries = append(r.entries, e)
}

func oauthAppRouter(h *Handler) *gin.Engine {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(middleware.OrganizationIDKey, uuid.New())
		c.Set(middleware.UserIDKey, uuid.NewString())
	})
	r.POST("/oauth/applications", h.CreateOAuthApplication)
	r.PATCH("/oauth/applications/:id", h.UpdateOAuthApplication)
	r.DELETE("/oauth/applications/:id", h.DeleteOAuthApplication)
	r.POST("/oauth/applications/:id/rotate-secret", h.RotateOAuthApplicationSecret)
	r.POST("/oauth/applications/:id/webhook-secret/rotate", h.RotateOAuthAppWebhookSecret)
	return r
}

func TestOAuthApplicationWritesAreAudited(t *testing.T) {
	gin.SetMode(gin.TestMode)
	app := &models.OAuthApplication{ID: uuid.New(), Name: "Sync", Status: models.OAuthAppActive, Scopes: models.APIPermReadContacts}
	tests := []struct {
		name, method, path, body string
		action                   models.AuditAction
		metadata                 map[string]string
	}{
		{"create", http.MethodPost, "/oauth/applications", `{"name":"Sync","scopes":1,"redirect_uris":["https://app.example/cb"]}`, models.AuditActionCreate, map[string]string{"name": "Sync"}},
		{"update", http.MethodPatch, "/oauth/applications/" + app.ID.String(), `{"name":"Sync two","scopes":1,"redirect_uris":["https://app.example/cb"]}`, models.AuditActionUpdate, nil},
		{"delete", http.MethodDelete, "/oauth/applications/" + app.ID.String(), "", models.AuditActionDelete, nil},
		{"rotate the client secret", http.MethodPost, "/oauth/applications/" + app.ID.String() + "/rotate-secret", "", models.AuditActionRotate, map[string]string{"secret": "client"}},
		{"rotate the webhook secret", http.MethodPost, "/oauth/applications/" + app.ID.String() + "/webhook-secret/rotate", "", models.AuditActionRotate, map[string]string{"secret": "webhook"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stored := *app
			repo := &fakeAppRepo{app: &stored}
			rec := &recordingAudit{}
			h := &Handler{OAuthService: oauth.NewService(repo, nil), AuditService: rec}
			w := httptest.NewRecorder()
			oauthAppRouter(h).ServeHTTP(w, httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body)))
			if w.Code >= http.StatusBadRequest {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if len(rec.entries) != 1 {
				t.Fatalf("audit entries = %d, want 1", len(rec.entries))
			}
			got := rec.entries[0]
			if got.action != tt.action || got.entity != models.AuditEntityOAuthApplication || got.id != repo.app.ID {
				t.Errorf("audited %s %s %s, want %s %s %s", got.action, got.entity, got.id, tt.action, models.AuditEntityOAuthApplication, repo.app.ID)
			}
			for k, v := range tt.metadata {
				if got.metadata[k] != v {
					t.Errorf("metadata[%q] = %q, want %q", k, got.metadata[k], v)
				}
			}
		})
	}
}

func TestOAuthApplicationFailedWriteIsNotAudited(t *testing.T) {
	gin.SetMode(gin.TestMode)
	app := &models.OAuthApplication{ID: uuid.New(), Name: "Sync"}
	rec := &recordingAudit{}
	h := &Handler{OAuthService: oauth.NewService(&fakeAppRepo{app: app, deleteErr: errors.New("down")}, nil), AuditService: rec}
	w := httptest.NewRecorder()
	oauthAppRouter(h).ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/oauth/applications/"+app.ID.String(), nil))
	if w.Code < http.StatusBadRequest {
		t.Fatalf("status %d, want a failure", w.Code)
	}
	if len(rec.entries) != 0 {
		t.Errorf("a failed delete was audited: %+v", rec.entries)
	}
}
