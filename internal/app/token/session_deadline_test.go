package token

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/infrastructure/cache"
	"github.com/warmbly/warmbly/internal/models"
)

// Past its expires_at a session answers no request and mints no token, even
// with an access and a refresh token whose own exp is still ahead.
func TestExpiredSessionNeitherAuthenticatesNorRefreshes(t *testing.T) {
	down := &cache.Cache{Client: redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 200 * time.Millisecond})}
	issued := time.Now().Add(-time.Hour).Truncate(time.Second)
	ended := time.Now().Add(-time.Minute)
	id, uid := uuid.New(), uuid.New()
	repo := &sessionRepo{session: &models.Session{ID: id, UserID: uid, ExpiresAt: &ended,
		AccessNonce: "access-nonce", RefreshNonce: "refresh-nonce", LastRefreshedAt: issued}}
	s := &tokenService{cache: down, tokenRepository: repo, AuthSecret: "test-only-signing-key-of-32-bytes-min"}
	ctx := context.Background()

	access, err := s.GenerateTokenFor(PurposeAccess, uid, id, "", "access-nonce", issued, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, xerr := s.ValidateAccessToken(ctx, access); xerr != errx.ErrSessionEnded {
		t.Fatalf("access token on an expired session: %v, want ErrSessionEnded", xerr)
	}
	refresh, err := s.GenerateTokenFor(PurposeRefresh, uid, id, "", "refresh-nonce", issued, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if tok, xerr := s.RefreshToken(ctx, refresh); xerr != errx.ErrSessionEnded || tok != nil {
		t.Fatalf("refresh on an expired session: %v, %v; want ErrSessionEnded and no token", tok, xerr)
	}

	live := time.Now().Add(time.Hour)
	repo.session.ExpiresAt = &live
	if _, xerr := s.ValidateAccessToken(ctx, access); xerr != nil {
		t.Fatalf("the same token on a live session: %v", xerr)
	}
}
