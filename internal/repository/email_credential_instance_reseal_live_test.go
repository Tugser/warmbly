package repository

import (
	"bytes"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/mailcred"
	"github.com/warmbly/warmbly/internal/pkg/encrypt"
)

// Under instance sealing, plaintext OAuth tokens are resealed under the instance key and still open to the same values.
func TestLiveInstanceResealSealsPlaintextOAuthTokens(t *testing.T) {
	handle, pool := liveContactDB(t)
	ctx := t.Context()
	f := newWarmupUsageFixture(t, pool)

	inst, err := encrypt.NewEncrypter(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	sealedRefresh, err := inst.Encrypt("refresh-token")
	if err != nil {
		t.Fatal(err)
	}
	f.exec(`INSERT INTO email_accounts_oauth (email_account_id, access_token, refresh_token, expires_at)
	        VALUES ($1, 'ya29.legacy-access', $2, $3)`, f.account, sealedRefresh, time.Now().Add(time.Hour))

	sealer, err := mailcred.NewSealer(inst, nil, mailcred.ModeInstance)
	if err != nil {
		t.Fatal(err)
	}
	r := NewEmailRepositoryWithSealer(handle, sealer).(*emailRepository)
	for cursor := uuid.Nil; ; {
		next, _, failed, err := r.ResealMailboxCredentials(ctx, cursor, 500)
		if err != nil || failed != 0 {
			t.Fatalf("reseal: failed=%d err=%v", failed, err)
		}
		if cursor = next; cursor == uuid.Nil {
			break
		}
	}

	var access, refresh string
	if err := pool.QueryRow(ctx, `SELECT access_token, refresh_token FROM email_accounts_oauth WHERE email_account_id = $1`, f.account).
		Scan(&access, &refresh); err != nil {
		t.Fatal(err)
	}
	if refresh != sealedRefresh {
		t.Fatal("an already sealed token was rewritten")
	}
	if got, err := inst.Decrypt(access); err != nil || got != "ya29.legacy-access" {
		t.Fatalf("access token opened as %q, %v", got, err)
	}
}
