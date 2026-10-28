package token

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/infrastructure/cache"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// failDel refuses every DEL while every other command reaches Redis.
type failDel struct{}

func (failDel) DialHook(next redis.DialHook) redis.DialHook { return next }
func (failDel) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "del" {
			return errors.New("injected: DEL refused")
		}
		return next(ctx, cmd)
	}
}
func (failDel) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func revocationCache(t *testing.T, refuseDel bool) *cache.Cache {
	t.Helper()
	url := os.Getenv("WARMBLY_TEST_REDIS")
	if url == "" {
		t.Skip("WARMBLY_TEST_REDIS not set")
	}
	c, err := cache.New(url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if refuseDel {
		c.AddHook(failDel{})
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// revocableRepo is the sessions table for one row. read, when set, blocks the
// row read until the test releases it, so a revocation can land in between.
type revocableRepo struct {
	repository.TokenRepository
	sess    models.Session
	read    chan struct{}
	started chan struct{}
}

func (r *revocableRepo) GetSession(context.Context, uuid.UUID) (*models.Session, *errx.Error) {
	row := r.sess
	if r.read != nil {
		close(r.started)
		<-r.read
		r.read = nil
	}
	return &row, nil
}

func (r *revocableRepo) RevokeSessionByID(_ context.Context, _, _ uuid.UUID, at time.Time) (bool, *errx.Error) {
	r.sess.RevokedAt = &at
	return true, nil
}

func liveRow(userID uuid.UUID) models.Session {
	exp := time.Now().Add(time.Hour)
	return models.Session{ID: uuid.New(), UserID: userID, ExpiresAt: &exp}
}

// A revoked session is refused even when the cache refuses the eviction.
func TestRevokedSessionIsRefusedWhenTheCacheEvictionFails(t *testing.T) {
	ctx := context.Background()
	user := uuid.New()
	repo := &revocableRepo{sess: liveRow(user)}
	s := &tokenService{cache: revocationCache(t, true), tokenRepository: repo}
	t.Cleanup(func() { _ = revocationCache(t, false).Del(ctx, getSessionKey(repo.sess.ID)).Err() })

	if got, _ := s.GetSession(ctx, repo.sess.ID); !got.Live(time.Now()) {
		t.Fatal("fixture: the session should start live and cached")
	}
	_ = s.RevokeSessionByID(ctx, user, repo.sess.ID, uuid.New())

	got, xerr := s.GetSession(ctx, repo.sess.ID)
	if xerr != nil {
		t.Fatalf("read after revoke: %v", xerr)
	}
	if got.Live(time.Now()) {
		t.Fatal("a revoked session is still served live from the cache")
	}
}

// A row read before a revocation commits is never cached after it.
func TestRevokedSessionIsNotRecachedByARacingRead(t *testing.T) {
	ctx := context.Background()
	user := uuid.New()
	repo := &revocableRepo{sess: liveRow(user), read: make(chan struct{}), started: make(chan struct{})}
	s := &tokenService{cache: revocationCache(t, false), tokenRepository: repo}
	t.Cleanup(func() { _ = s.cache.Del(ctx, getSessionKey(repo.sess.ID)).Err() })

	done := make(chan struct{})
	go func() { defer close(done); _, _ = s.GetSession(ctx, repo.sess.ID) }()
	<-repo.started // the racing request has read the live row
	if xerr := s.RevokeSessionByID(ctx, user, repo.sess.ID, uuid.New()); xerr != nil {
		t.Fatalf("revoke: %v", xerr)
	}
	close(repo.read) // and only now writes it to the cache
	<-done

	got, xerr := s.GetSession(ctx, repo.sess.ID)
	if xerr != nil {
		t.Fatalf("read after revoke: %v", xerr)
	}
	if got.Live(time.Now()) {
		t.Fatal("a racing read re-cached the live row after the revocation")
	}
}
