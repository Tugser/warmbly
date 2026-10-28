package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/infrastructure/cache"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// unreachableCache points at a port nothing listens on, so every command errors.
func unreachableCache(t *testing.T) *cache.Cache {
	t.Helper()
	c := &cache.Cache{Client: redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:1",
		DialTimeout: 50 * time.Millisecond,
		MaxRetries:  -1,
	})}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestAttemptBudgetsRefuseWhenTheCacheIsDown(t *testing.T) {
	s := svc(banUsers{}, "")
	s.cache = unreachableCache(t)
	ctx := context.Background()

	checks := map[string]func() (bool, *errx.Error){
		"password":   func() (bool, *errx.Error) { return s.reserveLoginAttempt(ctx, "a@example.com") },
		"email code": func() (bool, *errx.Error) { return s.reserveCodeAttempt(ctx, "login_sess:x", time.Minute) },
		"sso link":   func() (bool, *errx.Error) { return s.reserveSSOLinkAttempt(ctx, uuid.New(), time.Minute) },
		"reauth":     func() (bool, *errx.Error) { return s.ReserveReauthAttempt(ctx, uuid.New()) },
	}
	for name, reserve := range checks {
		ok, xerr := reserve()
		if ok || xerr == nil || xerr.Code != errx.ServiceUnavailable {
			t.Errorf("%s: got ok=%v err=%v, want a 503 refusal", name, ok, xerr)
		}
	}
}

type banLookupFails struct{ repository.UserRepository }

func (banLookupFails) GetBanState(context.Context, uuid.UUID) (uint32, error) {
	return 0, errors.New("database unavailable")
}

func TestSignInRefusesWhenTheBanStateCannotBeRead(t *testing.T) {
	if xerr := svc(banLookupFails{}, "").refuseSuspended(context.Background(), uuid.New()); xerr == nil {
		t.Fatal("a sign-in went through without a readable ban state")
	}
	if xerr := svc(banUsers{scope: uint32(models.BanScopeLogin)}, "").refuseSuspended(context.Background(), uuid.New()); xerr == nil || xerr.Code != errx.Forbidden {
		t.Fatalf("a login-banned account was not refused: %v", xerr)
	}
	if xerr := svc(banUsers{}, "").refuseSuspended(context.Background(), uuid.New()); xerr != nil {
		t.Fatalf("an account in good standing was refused: %v", xerr)
	}
}
