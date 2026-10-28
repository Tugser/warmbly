package safehttp

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// Hosted production ignores WARMBLY_ALLOW_UNSAFE_WEBHOOK_URLS at dial time; a self-host honours it.
func TestTransportIgnoresUnsafeFlagOnHostedProduction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	get := func() (*http.Response, error) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		return (&http.Client{Timeout: time.Second, Transport: NewTransport(u.Port())}).Do(req)
	}

	t.Setenv("WARMBLY_ALLOW_UNSAFE_WEBHOOK_URLS", "true")
	t.Setenv("DEPLOYMENT_MODE", "cloud")
	t.Setenv("APP_ENV", "production")
	res, err := get()
	if err == nil {
		_ = res.Body.Close()
	}
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("hosted production reached a loopback address: %v", err)
	}

	t.Setenv("DEPLOYMENT_MODE", "self_hosted")
	res, err = get()
	if err != nil {
		t.Fatalf("self-host opt-in refused: %v", err)
	}
	_ = res.Body.Close()
}
