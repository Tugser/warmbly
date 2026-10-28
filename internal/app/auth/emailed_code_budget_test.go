package auth

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/config"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/pkg/crypt"
)

// An emailed code is six random digits.
func TestVerificationCodeIsSixDigits(t *testing.T) {
	six := regexp.MustCompile(`^[0-9]{6}$`)
	seen := map[string]bool{}
	for range 2000 {
		code, err := crypt.VerificationCode()
		if err != nil || !six.MatchString(code) {
			t.Fatalf("code %q, err %v", code, err)
		}
		seen[code] = true
	}
	if len(seen) < 1990 {
		t.Fatalf("%d distinct codes in 2000 draws", len(seen))
	}
}

// One emailed-code session admits AuthAttempts comparisons, and one success.
func TestEmailedCodeSessionAdmitsThreeTriesAndOneUse(t *testing.T) {
	ctx := context.Background()
	s := trustedSvc(t, config.LoginCodeAlways)
	key := getLoginSessionKey(uuid.New())
	if err := s.cache.Set(ctx, key, "{}", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.cache.Del(ctx, key, key+":tries").Err() })

	for i := range AuthAttempts {
		if ok, xerr := s.reserveCodeAttempt(ctx, key, time.Minute); !ok || xerr != nil {
			t.Fatalf("try %d refused: %v", i+1, xerr)
		}
	}
	if ok, xerr := s.reserveCodeAttempt(ctx, key, time.Minute); ok || xerr != nil {
		t.Fatalf("try %d admitted (err %v), want refused", AuthAttempts+1, xerr)
	}
	if !s.consumeCodeSession(ctx, key) {
		t.Fatal("the first consume did not win")
	}
	if s.consumeCodeSession(ctx, key) {
		t.Fatal("a consumed code session was consumed again")
	}
}

// The send budget refuses the sixth code in the window and refuses on a cache error.
func TestEmailedCodeSendBudget(t *testing.T) {
	ctx := context.Background()
	s := trustedSvc(t, config.LoginCodeAlways)
	addr := uuid.NewString() + "@example.test"
	t.Cleanup(func() { _ = s.cache.Del(ctx, getEmailVerificationKey(emailFlowLogin, addr)).Err() })
	for i := range AuthEmailLimit {
		if xerr := s.canSendEmail(ctx, emailFlowLogin, addr); xerr != nil {
			t.Fatalf("send %d refused: %v", i+1, xerr)
		}
	}
	if xerr := s.canSendEmail(ctx, emailFlowLogin, addr); xerr != errx.ErrAuthLimit {
		t.Fatalf("send %d: %v, want ErrAuthLimit", AuthEmailLimit+1, xerr)
	}
	s.cache = unreachableCache(t)
	if xerr := s.canSendEmail(ctx, emailFlowLogin, addr); xerr == nil {
		t.Fatal("a code was sent with no readable send budget")
	}
}
