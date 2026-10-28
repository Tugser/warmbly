package middleware

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

// Node-only routes (DEK, message map, sync lookups) answer the node token and never the edge token tracking and forms carry.
func TestNodeRoutesRefuseTheEdgeToken(t *testing.T) {
	for _, tc := range []struct {
		name, internal, broker, accept, bearer string
		code                                   int
	}{
		{"node token is accepted", "edge", "node", "", "node", http.StatusOK},
		{"edge token is refused", "edge", "node", "", "edge", http.StatusUnauthorized},
		{"edge token is admitted only while an operator opts in", "edge", "node", "true", "edge", http.StatusOK},
		{"no token is refused", "edge", "node", "", "", http.StatusUnauthorized},
		{"nothing configured is refused", "", "", "", "anything", http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("INTERNAL_API_TOKEN", tc.internal)
			t.Setenv("NODE_BROKER_TOKEN", tc.broker)
			t.Setenv("NODE_ACCEPT_INTERNAL_TOKEN", tc.accept)
			brokerTokenOnce = sync.Once{}
			brokerToken = nil
			r := gin.New()
			r.GET("/api/v1/internal/dek/:orgID", (&Handler{}).NodeAuthMiddleware(), func(c *gin.Context) { c.Status(http.StatusOK) })
			req := httptest.NewRequest(http.MethodGet, "/api/v1/internal/dek/00000000-0000-0000-0000-000000000001", nil)
			if tc.bearer != "" {
				req.Header.Set("Authorization", "Bearer "+tc.bearer)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.code {
				t.Fatalf("status = %d, want %d", w.Code, tc.code)
			}
		})
	}
}
