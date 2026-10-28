package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func singleValuedRouter(called *bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestIDMiddleware(), RequestHeaders(), SingleValuedQuery())
	ok := func(c *gin.Context) {
		*called = true
		c.Status(http.StatusOK)
	}
	r.GET("/v1/campaigns", ok)
	r.GET("/v1/advisor/recommendations", ok)
	r.GET("/v1/analytics/dashboard", ok)
	return r
}

func TestSingleValuedQueryRefusesRepeatedParameters(t *testing.T) {
	for _, target := range []string{
		"/v1/campaigns?cursor=a&cursor=b",
		"/v1/campaigns?limit=5&q=x&limit=6",
		"/v1/advisor/recommendations?status=open&limit=1&limit=2",
		"/v1/analytics/dashboard?campaign_ids=a&from=1&from=2",
	} {
		t.Run(target, func(t *testing.T) {
			called := false
			w := httptest.NewRecorder()
			singleValuedRouter(&called).ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
			if called || w.Code != http.StatusBadRequest {
				t.Fatalf("called = %t, status = %d; want false, 400", called, w.Code)
			}
			body := w.Body.String()
			if !strings.Contains(body, `"duplicate_query_parameter"`) || !strings.Contains(body, `"request_id"`) {
				t.Errorf("missing stable code or request id: %s", body)
			}
		})
	}
}

func TestSingleValuedQueryKeepsListParameters(t *testing.T) {
	for _, target := range []string{
		"/v1/campaigns?q=one&limit=5",
		"/v1/advisor/recommendations?status=open&status=snoozed",
		"/v1/analytics/dashboard?campaign_ids=a&campaign_ids=b&folder_ids=c&folder_ids=d",
	} {
		t.Run(target, func(t *testing.T) {
			called := false
			w := httptest.NewRecorder()
			singleValuedRouter(&called).ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
			if !called || w.Code != http.StatusOK {
				t.Fatalf("called = %t, status = %d; want true, 200", called, w.Code)
			}
		})
	}
}

func TestRequestHeadersRefusesRepeatedCredentials(t *testing.T) {
	for _, name := range []string{"Authorization", "X-Warmbly-Token", "Idempotency-Key", "Origin"} {
		t.Run(name, func(t *testing.T) {
			called := false
			req := httptest.NewRequest(http.MethodGet, "/v1/campaigns", nil)
			req.Header.Add(name, "one")
			req.Header.Add(name, "two")
			w := httptest.NewRecorder()
			singleValuedRouter(&called).ServeHTTP(w, req)
			if called || w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"duplicate_header"`) {
				t.Fatalf("called = %t, status = %d, body = %s", called, w.Code, w.Body.String())
			}
		})
	}
}

func TestRequestHeadersResolvesForwardingHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	if err := r.SetTrustedProxies([]string{"192.0.2.0/24"}); err != nil {
		t.Fatal(err)
	}
	r.Use(RequestIDMiddleware(), RequestHeaders())
	var ip, proto string
	r.GET("/", func(c *gin.Context) {
		ip, proto = c.ClientIP(), c.GetHeader("X-Forwarded-Proto")
		c.Status(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.0.2.10:1234"
	req.Header.Add("X-Forwarded-For", "203.0.113.66")
	req.Header.Add("X-Forwarded-For", "198.51.100.7")
	req.Header.Add("X-Forwarded-Proto", "http")
	req.Header.Add("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || ip != "198.51.100.7" || proto != "https" {
		t.Fatalf("status = %d, ip = %q, proto = %q; want 200, 198.51.100.7, https", w.Code, ip, proto)
	}
}
