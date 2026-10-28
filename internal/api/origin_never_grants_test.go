package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/api/handler"
	"github.com/warmbly/warmbly/internal/api/middleware"
	"github.com/warmbly/warmbly/internal/app/token"
	"github.com/warmbly/warmbly/internal/app/user"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

const goodToken = "valid-access-token"

type countingTokens struct {
	token.TokenService
	user  uuid.UUID
	calls *int
}

func (f countingTokens) ValidateAccessToken(_ context.Context, tok string) (*models.Session, *errx.Error) {
	*f.calls++
	if tok != goodToken {
		return nil, errx.ErrAuth
	}
	return &models.Session{ID: uuid.New(), UserID: f.user}, nil
}

type oneUser struct{ user.UserService }

func (oneUser) GetUser(_ context.Context, id uuid.UUID) (*models.User, *errx.Error) {
	return &models.User{ID: id}, nil
}

// Origin can refuse a browser request before any credential is read; it never stands in for one.
func TestOriginOnlyRefusesAndTheCredentialDecides(t *testing.T) {
	const trusted = "https://app.example.test"
	calls := 0
	m := &middleware.Handler{TokenService: countingTokens{user: uuid.New(), calls: &calls}}
	r := Run(&handler.Handler{UserService: oneUser{}}, m, &middleware.OidcHandler{}, "", gin.ReleaseMode, []string{trusted})
	for _, tc := range []struct {
		name, origin, bearer string
		code                 int
		authRead             bool
	}{
		{"untrusted origin with a valid credential is refused before auth", "https://evil.example.test", goodToken, http.StatusForbidden, false},
		{"untrusted origin with no credential is refused before auth", "https://evil.example.test", "", http.StatusForbidden, false},
		{"trusted origin with no credential is unauthenticated", trusted, "", http.StatusUnauthorized, false},
		{"trusted origin with a wrong credential is unauthenticated", trusted, "forged", http.StatusUnauthorized, true},
		{"no origin with no credential is unauthenticated", "", "", http.StatusUnauthorized, false},
		{"no origin with a valid credential is served", "", goodToken, http.StatusOK, true},
		{"trusted origin with a valid credential is served", trusted, goodToken, http.StatusOK, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls = 0
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/auth/me", nil)
			req.Host = "api.example.test"
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.bearer != "" {
				req.Header.Set("Authorization", "Bearer "+tc.bearer)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.code {
				t.Fatalf("status = %d, want %d; body=%s", w.Code, tc.code, w.Body.String())
			}
			if (calls > 0) != tc.authRead {
				t.Fatalf("credential read = %v, want %v", calls > 0, tc.authRead)
			}
		})
	}
}
