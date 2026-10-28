package token

import (
	"testing"
	"time"

	"github.com/warmbly/warmbly/internal/models"
)

// A session's expires_at is an absolute deadline: past it the session neither
// authenticates nor refreshes, and no token it issues outlives it.
func TestSessionExpiryIsAbsolute(t *testing.T) {
	now := time.Now()
	past, future := now.Add(-time.Minute), now.Add(time.Hour)

	cases := []struct {
		name string
		sess *models.Session
		live bool
	}{
		{"unexpired", &models.Session{ExpiresAt: &future}, true},
		{"expired", &models.Session{ExpiresAt: &past}, false},
		{"expires exactly now", &models.Session{ExpiresAt: &now}, false},
		{"revoked before expiry", &models.Session{ExpiresAt: &future, RevokedAt: &past}, false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.sess.Live(now); got != tc.live {
				t.Fatalf("Live = %v, want %v", got, tc.live)
			}
		})
	}

	sess := &models.Session{ExpiresAt: &future}
	if got := capAtSessionExpiry(now.Add(RefreshTokenLifeTime), sess); !got.Equal(future) {
		t.Fatalf("a rotated refresh token expires at %v, want the session's %v", got, future)
	}
	if got := capAtSessionExpiry(now.Add(time.Minute), sess); !got.Equal(now.Add(time.Minute)) {
		t.Fatalf("a token ending before the session keeps its own expiry, got %v", got)
	}
}
