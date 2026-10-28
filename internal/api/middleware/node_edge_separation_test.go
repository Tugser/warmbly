package middleware

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

// With NODE_BROKER_TOKEN set, the token the tracking and forms services hold opens no node route.
func TestEdgeAndNodeTokensOpenOnlyTheirOwnRouteGroups(t *testing.T) {
	t.Setenv("INTERNAL_API_TOKEN", "edge-token")
	t.Setenv("NODE_BROKER_TOKEN", "node-token")
	t.Setenv("NODE_ACCEPT_INTERNAL_TOKEN", "")
	internalTokenOnce, brokerTokenOnce = sync.Once{}, sync.Once{}
	t.Cleanup(func() {
		internalTokenOnce, brokerTokenOnce = sync.Once{}, sync.Once{}
		internalToken, brokerToken = nil, nil
	})
	h := &Handler{}
	r := gin.New()
	ok := func(c *gin.Context) { c.Status(http.StatusOK) }
	r.GET("/edge", h.InternalAuthMiddleware(), ok)
	r.GET("/broker", h.NodeBrokerAuthMiddleware(), ok)
	r.GET("/node", h.NodeAuthMiddleware(), ok)
	for _, tc := range []struct {
		path, token string
		want        int
	}{
		{"/edge", "edge-token", 200},
		{"/edge", "node-token", 401},
		{"/broker", "edge-token", 401},
		{"/node", "edge-token", 401},
		{"/broker", "node-token", 200},
		{"/node", "node-token", 200},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		req.Header.Set("Authorization", "Bearer "+tc.token)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s with %s: %d, want %d", tc.path, tc.token, rec.Code, tc.want)
		}
	}
}
