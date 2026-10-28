package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// A panic answers 500 with no body: the value and the stack go to the report only.
func TestRecoveryAnswersWithoutTheStack(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(Recovery())
	r.GET("/boom", func(*gin.Context) { panic("internal-detail-marker") })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if b := rec.Body.String(); strings.Contains(b, "internal-detail-marker") || strings.Contains(b, "goroutine") || strings.Contains(b, ".go:") {
		t.Fatalf("panic detail reached the client: %q", b)
	}
}
