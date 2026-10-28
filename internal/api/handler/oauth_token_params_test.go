package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func postTokenForm(t *testing.T, fn gin.HandlerFunc, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/oauth/token", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	fn(c)
	return w
}

// RFC 6749 section 3.2: a parameter sent more than once is refused, whichever it is.
func TestOAuthTokenEndpointsRefuseARepeatedParameter(t *testing.T) {
	h := &Handler{}
	for _, body := range []string{
		"grant_type=authorization_code&grant_type=refresh_token",
		"grant_type=authorization_code&code=a&code=b",
		"grant_type=refresh_token&refresh_token=a&client_id=x&client_id=y",
	} {
		for name, fn := range map[string]gin.HandlerFunc{"token": h.OAuthToken, "revoke": h.OAuthRevoke} {
			w := postTokenForm(t, fn, body)
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"error":"invalid_request"`) {
				t.Errorf("%s %q = %d %s", name, body, w.Code, w.Body.String())
			}
		}
	}

	// A form with every parameter once reaches the grant dispatch.
	w := postTokenForm(t, h.OAuthToken, "grant_type=password&username=a")
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"error":"unsupported_grant_type"`) {
		t.Fatalf("single-valued form = %d %s", w.Code, w.Body.String())
	}
}
