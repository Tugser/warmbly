package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// /public serves only the public prefixes; a private key of any workspace answers 404 before storage is consulted.
func TestServePublicObjectRefusesPrivateKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &Handler{} // no storage: a key that passes the prefix check answers 503, one that fails answers 404
	r.GET("/public/*key", h.ServePublicObject)
	for _, tc := range []struct {
		key  string
		want int
	}{
		{"attachments/0b7d5c1e-6a1f-4c1e-9d5a-2f0c8e4b7a10/f.pdf", http.StatusNotFound},
		{"emails/0b7d5c1e-6a1f-4c1e-9d5a-2f0c8e4b7a10/body.eml", http.StatusNotFound},
		{"users/0b7d5c1e-6a1f-4c1e-9d5a-2f0c8e4b7a10/export.zip", http.StatusNotFound},
		{"bodies/0b7d5c1e-6a1f-4c1e-9d5a-2f0c8e4b7a10", http.StatusNotFound},
		{"avatars/../attachments/x.pdf", http.StatusNotFound},
		{"email-images/../users/x", http.StatusNotFound},
		{"avatars/u.png", http.StatusServiceUnavailable},
		{"email-images/i.png", http.StatusServiceUnavailable},
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/public/"+tc.key, nil))
		if rec.Code != tc.want {
			t.Errorf("%s: status %d, want %d", tc.key, rec.Code, tc.want)
		}
	}
}
