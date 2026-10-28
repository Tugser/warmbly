package auth

import (
	"context"
	"testing"

	"github.com/google/uuid"
	tokenpkg "github.com/warmbly/warmbly/internal/app/token"
	"github.com/warmbly/warmbly/internal/config"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/pkg/argon2"
	"github.com/warmbly/warmbly/internal/repository"
)

type orderLog []string

type pwRepo struct {
	repository.AuthRepository
	hash string
	log  *orderLog
}

func (r *pwRepo) GetPasswordHash(context.Context, uuid.UUID) (string, *errx.Error) {
	return r.hash, nil
}
func (r *pwRepo) ResetPassword(context.Context, uuid.UUID, string) *errx.Error {
	*r.log = append(*r.log, "store")
	return nil
}

type pwTokens struct {
	tokenpkg.TokenService
	revokeErr *errx.Error
	log       *orderLog
}

func (t *pwTokens) RevokeOtherSessions(context.Context, uuid.UUID, uuid.UUID) *errx.Error {
	*t.log = append(*t.log, "revoke")
	return t.revokeErr
}

func (t *pwTokens) ReissueSession(context.Context, uuid.UUID, *models.Session, string, string) (*models.Token, *errx.Error) {
	return &models.Token{}, nil
}

// A signed-in password change ends every other session before the new password
// is stored, and stores nothing when that revocation or the optional credential
// revocation fails.
func TestChangePasswordRevokesBeforeStoring(t *testing.T) {
	const current, next = "old-Passphrase-4471", "a-much-longer-new-passphrase-9083"
	hash, err := argon2.Hash(current)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	change := func(revokeErr *errx.Error, revokeCreds bool, revokers ...UserCredentialRevoker) (orderLog, *errx.Error) {
		var log orderLog
		s := trustedSvc(t, config.LoginCodeNewDevice)
		s.authRepository = &pwRepo{hash: hash, log: &log}
		s.tokenService = &pwTokens{revokeErr: revokeErr, log: &log}
		s.WireCredentialRevokers(revokers...)
		uid := uuid.New()
		t.Cleanup(func() { s.ClearReauthFailures(ctx, uid) })
		_, xerr := s.ChangePassword(ctx, uid, &models.Session{ID: uuid.New(), UserID: uid}, "127.0.0.1", "test",
			&ChangePassword{CurrentPassword: current, NewPassword: next, RevokeAPICredentials: revokeCreds})
		return log, xerr
	}

	log, xerr := change(nil, false)
	if xerr != nil || len(log) != 2 || log[0] != "revoke" || log[1] != "store" {
		t.Fatalf("order = %v, err = %v; want [revoke store]", log, xerr)
	}

	log, xerr = change(errx.InternalError(), false)
	if xerr == nil || xerr.Identifier != ErrSessionsNotEnded.Identifier {
		t.Fatalf("a failed revocation answered %v, want sessions_not_ended", xerr)
	}
	for _, step := range log {
		if step == "store" {
			t.Fatal("the password was stored although other sessions could not be ended")
		}
	}

	log, xerr = change(nil, true, &fakeRevoker{err: context.DeadlineExceeded})
	if xerr == nil {
		t.Fatal("a failed app and API key revocation must refuse the change")
	}
	for _, step := range log {
		if step == "store" {
			t.Fatal("the password was stored although apps and API keys could not be signed out")
		}
	}

	grants, keys := &fakeRevoker{}, &fakeRevoker{}
	if _, xerr = change(nil, true, grants, keys); xerr != nil || grants.calls != 1 || keys.calls != 1 {
		t.Fatalf("revoke_api_credentials: err %v, grants %d, keys %d", xerr, grants.calls, keys.calls)
	}
}
