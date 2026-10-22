package middleware

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestTokenRotationGraceIsScopedAndExpires(t *testing.T) {
	t.Setenv("INTERNAL_API_TOKEN", "internal-current")
	t.Setenv("NODE_BROKER_TOKEN", "broker-current")
	t.Setenv("INTERNAL_API_PREVIOUS_TOKENS", `["internal-previous"]`)
	t.Setenv("NODE_BROKER_PREVIOUS_TOKENS", `["broker-previous"]`)
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	t.Setenv("INTERNAL_API_PREVIOUS_UNTIL", future)
	t.Setenv("NODE_BROKER_PREVIOUS_UNTIL", future)
	internalTokenOnce = sync.Once{}
	brokerTokenOnce = sync.Once{}
	t.Cleanup(func() {
		internalTokenOnce = sync.Once{}
		brokerTokenOnce = sync.Once{}
		internalToken = nil
		brokerToken = nil
	})
	h := &Handler{}
	r := gin.New()
	r.GET("/internal", h.InternalAuthMiddleware(), func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/broker", h.NodeBrokerAuthMiddleware(), func(c *gin.Context) { c.Status(http.StatusOK) })
	request := func(path, token string) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		out := httptest.NewRecorder()
		r.ServeHTTP(out, req)
		return out.Code
	}
	for _, tc := range []struct {
		path, token string
		want        int
	}{{"/internal", "internal-current", 200}, {"/internal", "internal-previous", 200}, {"/internal", "broker-previous", 401}, {"/broker", "broker-current", 200}, {"/broker", "broker-previous", 200}, {"/broker", "internal-previous", 401}} {
		if got := request(tc.path, tc.token); got != tc.want {
			t.Fatalf("scope %s: got %d, want %d", tc.path, got, tc.want)
		}
	}
	past := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	t.Setenv("INTERNAL_API_PREVIOUS_UNTIL", past)
	t.Setenv("NODE_BROKER_PREVIOUS_UNTIL", past)
	if request("/internal", "internal-previous") != 401 || request("/broker", "broker-previous") != 401 {
		t.Fatal("previous tokens must expire without restarting")
	}
	if request("/internal", "internal-current") != 200 || request("/broker", "broker-current") != 200 {
		t.Fatal("expiry must preserve current credentials")
	}
	t.Setenv("INTERNAL_API_PREVIOUS_UNTIL", future)
	t.Setenv("INTERNAL_API_PREVIOUS_TOKENS", `["sensitive-value",5]`)
	if request("/internal", "sensitive-value") != 401 {
		t.Fatal("malformed overlap configuration must fail closed")
	}
}
