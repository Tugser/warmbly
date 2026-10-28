package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type mockCustomDomains struct {
	verified map[string]bool
	err      error
	calls    int
}

func (m *mockCustomDomains) IsVerified(_ context.Context, host string) (bool, error) {
	m.calls++
	if m.err != nil {
		return false, m.err
	}
	return m.verified[host], nil
}

func (m *mockCustomDomains) Owners(context.Context, string) ([]uuid.UUID, error) { return nil, nil }

func newTLSRouter(repo *mockCustomDomains) *gin.Engine {
	h := &Handler{CustomDomains: repo}
	r := gin.New()
	r.GET("/tls/authorize", h.AuthorizeTLSDomain)
	return r
}

// resetTLSDomainCache keeps one test's decisions from answering another's.
func resetTLSDomainCache(t *testing.T) {
	t.Helper()
	tlsDomainCache.mu.Lock()
	tlsDomainCache.entries = map[string]domainDecision{}
	tlsDomainCache.mu.Unlock()
}

func ask(t *testing.T, r *gin.Engine, domain string) int {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/tls/authorize?domain="+domain, nil)
	r.ServeHTTP(w, req)
	return w.Code
}

func TestAuthorizeTLSDomain(t *testing.T) {
	t.Setenv("TRACKING_DOMAIN", "track.selfhost.com")
	t.Setenv("FORMS_DOMAIN", "")

	repo := &mockCustomDomains{verified: map[string]bool{"t.acme.com": true}}
	r := newTLSRouter(repo)

	cases := []struct {
		name   string
		domain string
		want   int
	}{
		// The bug in #400: a verified custom tracking domain had no
		// certificate, so every tracked link on it failed TLS.
		{"verified custom domain", "t.acme.com", http.StatusNoContent},
		// Normalization is shared with the verification path, so what the
		// customer stored and what arrives in SNI compare equal.
		{"case and trailing dot", "T.Acme.Com.", http.StatusNoContent},
		{"this install's own tracking host", "track.selfhost.com", http.StatusNoContent},
		{"unknown domain", "evil.example.com", http.StatusForbidden},
		{"no domain at all", "", http.StatusForbidden},
		// Rejected before any database work.
		{"not a hostname", "not-a-host", http.StatusForbidden},
		{"empty label", "a..b.com", http.StatusForbidden},
		{"leading hyphen", "-bad.example.com", http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetTLSDomainCache(t)
			if got := ask(t, r, tc.domain); got != tc.want {
				t.Fatalf("ask(%q) = %d, want %d", tc.domain, got, tc.want)
			}
		})
	}
}

// A lookup failure must refuse rather than issue, and must not be remembered:
// Caddy retries on the next handshake, and a cached no from a transient
// database error would outlive the error.
func TestAuthorizeTLSDomainLookupError(t *testing.T) {
	t.Setenv("TRACKING_DOMAIN", "track.selfhost.com")
	resetTLSDomainCache(t)

	repo := &mockCustomDomains{err: errors.New("db down")}
	r := newTLSRouter(repo)

	if got := ask(t, r, "t.acme.com"); got != http.StatusForbidden {
		t.Fatalf("got %d, want %d", got, http.StatusForbidden)
	}
	if _, cached := tlsDomainCache.get("t.acme.com"); cached {
		t.Fatal("a lookup error was cached")
	}
}

func TestAuthorizeTLSDomainCaches(t *testing.T) {
	t.Setenv("TRACKING_DOMAIN", "track.selfhost.com")
	resetTLSDomainCache(t)

	repo := &mockCustomDomains{verified: map[string]bool{"t.acme.com": true}}
	r := newTLSRouter(repo)

	for i := 0; i < 3; i++ {
		if got := ask(t, r, "t.acme.com"); got != http.StatusNoContent {
			t.Fatalf("call %d: got %d", i, got)
		}
	}
	if repo.calls != 1 {
		t.Fatalf("hit the database %d times, want 1", repo.calls)
	}

	// A refusal is cached too, so an SNI sprayer repeating one name does not
	// repeat the lookup.
	for i := 0; i < 3; i++ {
		if got := ask(t, r, "evil.example.com"); got != http.StatusForbidden {
			t.Fatalf("call %d: got %d", i, got)
		}
	}
	if repo.calls != 2 {
		t.Fatalf("hit the database %d times, want 2", repo.calls)
	}
}

// The cache is bounded: spraying distinct names cannot grow it without limit.
func TestTLSDomainCacheIsBounded(t *testing.T) {
	c := &domainCache{
		entries: map[string]domainDecision{},
		allowed: time.Minute,
		refused: time.Minute,
		max:     16,
	}
	for i := 0; i < 200; i++ {
		c.put(string(rune('a'+i%26))+string(rune('a'+i/26))+".example.com", false)
	}
	c.mu.Lock()
	n := len(c.entries)
	c.mu.Unlock()
	if n > c.max {
		t.Fatalf("cache grew to %d entries, max is %d", n, c.max)
	}
}

// A refusal expires quickly so a domain that has just verified gets its
// certificate on the next handshake rather than after the customer waits.
func TestTLSDomainCacheExpiry(t *testing.T) {
	c := &domainCache{
		entries: map[string]domainDecision{},
		allowed: time.Minute,
		refused: -time.Second,
		max:     16,
	}
	c.put("t.acme.com", false)
	if _, ok := c.get("t.acme.com"); ok {
		t.Fatal("an expired refusal was served from the cache")
	}
}

// A nil repository must refuse rather than panic: a deployment with no
// database wired into the handler still answers the probe.
func TestAuthorizeTLSDomainWithoutRepository(t *testing.T) {
	t.Setenv("TRACKING_DOMAIN", "track.selfhost.com")
	resetTLSDomainCache(t)

	h := &Handler{}
	r := gin.New()
	r.GET("/tls/authorize", h.AuthorizeTLSDomain)

	if got := ask(t, r, "t.acme.com"); got != http.StatusForbidden {
		t.Fatalf("got %d, want %d", got, http.StatusForbidden)
	}
}
