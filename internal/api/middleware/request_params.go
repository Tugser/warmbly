package middleware

import (
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/warmbly/warmbly/internal/errx"
)

// RepeatableQuery lists, by route pattern, the query parameters a handler reads
// as a list. "*" passes every parameter through (a proxied upstream decides).
var RepeatableQuery = map[string][]string{
	"/v1/advisor/recommendations": {"status"},
	"/v1/analytics/dashboard":     {"campaign_ids", "folder_ids"},
	"/ingest/*path":               {"*"},
}

// SingleValuedQuery refuses a query that names a parameter more than once,
// except one its route reads as a list (RepeatableQuery).
func SingleValuedQuery() gin.HandlerFunc {
	return func(c *gin.Context) {
		if name, dup := DuplicateQueryParam(c.Request.URL.RawQuery, RepeatableQuery[c.FullPath()]); dup {
			c.Abort()
			errx.JSON(c, errx.NewWithIdentifier(errx.BadRequest, "duplicate_query_parameter", DuplicateMessage("query parameter", name)))
			return
		}
		c.Next()
	}
}

// DuplicateQueryParam returns the first parameter raw names more than once that allowed does not list.
func DuplicateQueryParam(raw string, allowed []string) (string, bool) {
	if raw == "" {
		return "", false
	}
	if slices.Contains(allowed, "*") {
		return "", false
	}
	values, _ := url.ParseQuery(raw)
	keys := make([]string, 0, len(values))
	for k, v := range values {
		if len(v) > 1 && !slices.Contains(allowed, k) {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return "", false
	}
	sort.Strings(keys)
	return keys[0], true
}

// singleValuedHeaders authenticate, authorize or deduplicate a request; a second line is refused.
var singleValuedHeaders = []string{"Authorization", "X-Warmbly-Token", "Idempotency-Key", "Origin"}

// lastLineHeaders are single-valued forwarding headers; the line the nearest proxy wrote (the last) is kept.
var lastLineHeaders = []string{"X-Forwarded-Proto", "X-Forwarded-Host", "X-Forwarded-Port", "X-Real-Ip"}

// RequestHeaders refuses a repeated credential, idempotency or origin header,
// folds repeated X-Forwarded-For lines into one list in arrival order, and keeps
// only the last line of the other forwarding headers.
func RequestHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Request.Header
		for _, name := range singleValuedHeaders {
			if len(h.Values(name)) > 1 {
				c.Abort()
				errx.JSON(c, errx.NewWithIdentifier(errx.BadRequest, "duplicate_header", DuplicateMessage("header", name)))
				return
			}
		}
		normalizeForwardedHeaders(h)
		c.Next()
	}
}

// ForwardedHeaders is the forwarding-header half of RequestHeaders, for a service with its own error shape.
func ForwardedHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		normalizeForwardedHeaders(c.Request.Header)
		c.Next()
	}
}

func normalizeForwardedHeaders(h http.Header) {
	if v := h.Values("X-Forwarded-For"); len(v) > 1 {
		h.Set("X-Forwarded-For", strings.Join(v, ", "))
	}
	for _, name := range lastLineHeaders {
		if v := h.Values(name); len(v) > 1 {
			h.Set(name, v[len(v)-1])
		}
	}
}

var plainParamName = regexp.MustCompile(`^[A-Za-z0-9_.\[\]-]{1,64}$`)

// DuplicateMessage names the repeated parameter or header when its name is
// plain enough to echo back, and says how to fix the request.
func DuplicateMessage(kind, name string) string {
	if plainParamName.MatchString(name) {
		return "The " + kind + " " + name + " appears more than once. Send it once per request."
	}
	return "A " + kind + " appears more than once. Send each one once per request."
}
