package formserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/api/handler"
	"github.com/warmbly/warmbly/internal/app/customhost"
	"github.com/warmbly/warmbly/internal/models"
)

type fakeOwners map[string][]uuid.UUID

func (f fakeOwners) Owners(_ context.Context, host string) ([]uuid.UUID, error) { return f[host], nil }

// newHostFixture serves one form owned by formOrg, with forms.acme.test verified by acmeOrg.
func newHostFixture(t *testing.T, formOrg, acmeOrg uuid.UUID) (*Server, *gin.Engine) {
	t.Helper()
	t.Setenv("FORMS_DOMAIN", "forms.warmbly.test")
	gin.SetMode(gin.TestMode)
	stub := &stubFormService{form: &models.Form{
		ID:             uuid.New(),
		OrganizationID: formOrg,
		PublicID:       "pubtest123",
		Name:           "Demo request",
		Fields:         []models.FormField{{ID: "email", Type: models.FormFieldEmail, Label: "Work email", Required: true, MapTo: "email"}},
	}}
	h := &handler.Handler{FormService: stub, CustomHosts: customhost.New(fakeOwners{"forms.acme.test": {acmeOrg}}, nil, nil)}
	backend := gin.New()
	backend.GET("/api/v1/internal/forms/:publicID", h.InternalGetPublicForm)
	backend.POST("/api/v1/internal/forms/:publicID/events", h.InternalRecordFormEvent)
	backend.POST("/api/v1/internal/forms/:publicID/submissions", h.InternalSubmitForm)
	ts := httptest.NewServer(backend)
	t.Cleanup(ts.Close)

	srv, err := New(Config{BackendURL: ts.URL, InternalToken: "test-internal", StaticDir: writeStaticDir(t)})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	r, err := srv.Router(nil)
	if err != nil {
		t.Fatalf("router: %v", err)
	}
	return srv, r
}

func onHost(r *gin.Engine, method, host, path, renderToken string) int {
	req := httptest.NewRequest(method, path, nil)
	if method == http.MethodPost {
		req = httptest.NewRequest(method, path, strings.NewReader(`{"answers":{"email":["a@b.test"]}}`))
		req.Header.Set("Content-Type", "application/json")
	}
	req.Host = host
	if renderToken != "" {
		req.Header.Set("X-Warmbly-Render", renderToken)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func TestFormOnAnotherWorkspacesCustomHostIsNotFound(t *testing.T) {
	acme, other := uuid.New(), uuid.New()
	srv, r := newHostFixture(t, other, acme)
	token := mintFor(srv, "pubtest123")

	if code := onHost(r, http.MethodGet, "forms.acme.test", "/f/pubtest123", ""); code != http.StatusNotFound {
		t.Fatalf("shell on another workspace's host: %d, want 404", code)
	}
	if code := onHost(r, http.MethodGet, "forms.acme.test:443", "/api/forms/pubtest123", token); code != http.StatusNotFound {
		t.Fatalf("form JSON on another workspace's host: %d, want 404", code)
	}
	if code := onHost(r, http.MethodPost, "forms.acme.test", "/api/forms/pubtest123/submit", token); code != http.StatusNotFound {
		t.Fatalf("submit on another workspace's host: %d, want 404", code)
	}
	// The same form keeps answering on the platform host and on a host nobody verified.
	for _, host := range []string{"forms.warmbly.test", "unclaimed.example.test"} {
		if code := onHost(r, http.MethodGet, host, "/f/pubtest123", ""); code != http.StatusOK {
			t.Fatalf("shell on %s: %d, want 200", host, code)
		}
	}
}

func TestFormOnOwnCustomHostIsServed(t *testing.T) {
	acme := uuid.New()
	srv, r := newHostFixture(t, acme, acme)

	if code := onHost(r, http.MethodGet, "forms.acme.test", "/f/pubtest123", ""); code != http.StatusOK {
		t.Fatalf("shell on own host: %d, want 200", code)
	}
	if code := onHost(r, http.MethodPost, "forms.acme.test", "/api/forms/pubtest123/submit", mintFor(srv, "pubtest123")); code != http.StatusOK {
		t.Fatalf("submit on own host: %d, want 200", code)
	}
}
