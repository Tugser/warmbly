package repository

import (
	"bytes"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/cipher"
	"github.com/warmbly/warmbly/internal/app/mailcred"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/pkg/encrypt"
)

// Stored credentials move onto the workspace key and still open to the same values.
func TestLiveResealMovesMailboxCredentialsOntoOrganizationKeys(t *testing.T) {
	handle, pool := liveContactDB(t)
	ctx := t.Context()
	f := newWarmupUsageFixture(t, pool)

	inst, err := encrypt.NewEncrypter(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	seal := func(v string) string {
		s, err := inst.Encrypt(v)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	plain := []string{"smtp.x.test", "u@x.test", "smtp-pass", "imap.x.test", "", "imap-pass"}
	// An empty stored value is not ciphertext and must still move.
	f.exec(`INSERT INTO email_accounts_smtp_imap
	          (email_account_id, smtp_host, smtp_port, smtp_user, smtp_password, imap_host, imap_port, imap_user, imap_password)
	        VALUES ($1, $2, 465, $3, $4, $5, 993, '', $6)`,
		f.account, seal(plain[0]), seal(plain[1]), seal(plain[2]), seal(plain[3]), seal(plain[5]))
	// A token written before sealing existed is plaintext and must be moved too.
	f.exec(`INSERT INTO email_accounts_oauth (email_account_id, access_token, refresh_token, expires_at)
	        VALUES ($1, 'legacy-access', $2, $3)`, f.account, seal("refresh-token"), time.Now().Add(time.Hour))

	sealer, err := mailcred.NewSealer(inst, cipher.NewStatic(bytes.Repeat([]byte{9}, 32)), mailcred.ModeOrganization)
	if err != nil {
		t.Fatal(err)
	}
	r := NewEmailRepositoryWithSealer(handle, sealer).(*emailRepository)

	cursor := uuid.Nil
	for {
		next, _, failed, err := r.ResealMailboxCredentials(ctx, cursor, 500)
		if err != nil {
			t.Fatal(err)
		}
		if failed != 0 {
			t.Fatalf("%d mailboxes could not be resealed", failed)
		}
		if cursor = next; cursor == uuid.Nil {
			break
		}
	}

	var stored [8]string
	if err := pool.QueryRow(ctx, `SELECT s.smtp_host, s.smtp_user, s.smtp_password, s.imap_host, s.imap_user, s.imap_password,
	        o.access_token, o.refresh_token
	        FROM email_accounts_smtp_imap s JOIN email_accounts_oauth o USING (email_account_id)
	        WHERE email_account_id = $1`, f.account).
		Scan(&stored[0], &stored[1], &stored[2], &stored[3], &stored[4], &stored[5], &stored[6], &stored[7]); err != nil {
		t.Fatal(err)
	}
	want := append(append([]string{}, plain...), "legacy-access", "refresh-token")
	for i, v := range stored {
		if !mailcred.IsOrgSealed(v) {
			t.Fatalf("column %d is still on the instance key", i)
		}
		got, err := sealer.Open(ctx, &f.org, v)
		if err != nil || got != want[i] {
			t.Fatalf("column %d opened as %q, %v; want %q", i, got, err, want[i])
		}
	}

	creds, xerr := r.GetOAuthCredentials(ctx, f.account)
	if xerr != nil || creds.AccessToken != "legacy-access" || creds.RefreshToken != "refresh-token" {
		t.Fatalf("OAuth credentials read back as %+v, %v", creds, xerr)
	}
}

// Writes in organization mode land on the workspace key and read back unchanged.
func TestLiveOrganizationModeWritesReadBack(t *testing.T) {
	handle, pool := liveContactDB(t)
	ctx := t.Context()
	f := newWarmupUsageFixture(t, pool)
	f.exec(`INSERT INTO email_accounts_smtp_imap
	          (email_account_id, smtp_host, smtp_port, smtp_user, smtp_password, imap_host, imap_port, imap_user, imap_password)
	        VALUES ($1, '', 465, '', '', '', 993, '', '')`, f.account)
	f.exec(`INSERT INTO email_accounts_oauth (email_account_id, access_token, refresh_token, expires_at)
	        VALUES ($1, '', '', now())`, f.account)

	inst, err := encrypt.NewEncrypter(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := mailcred.NewSealer(inst, cipher.NewStatic(bytes.Repeat([]byte{9}, 32)), mailcred.ModeOrganization)
	if err != nil {
		t.Fatal(err)
	}
	r := NewEmailRepositoryWithSealer(handle, sealer)

	if err := r.ReplaceSMTPIMAPCredentials(ctx, f.account, &models.SmtpImap{
		SMTP: &models.Service{Host: "smtp.x.test", Port: 465, Username: "u@x.test", Password: "smtp-pass"},
		IMAP: &models.Service{Host: "imap.x.test", Port: 993, Username: "u@x.test", Password: "imap-pass"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.RefreshBoxToken(ctx, f.account, "access", "refresh", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	var password, refresh string
	if err := pool.QueryRow(ctx, `SELECT s.smtp_password, o.refresh_token FROM email_accounts_smtp_imap s
	        JOIN email_accounts_oauth o USING (email_account_id) WHERE email_account_id = $1`, f.account).Scan(&password, &refresh); err != nil {
		t.Fatal(err)
	}
	if !mailcred.IsOrgSealed(password) || !mailcred.IsOrgSealed(refresh) {
		t.Fatal("organization mode wrote a credential under the instance key")
	}

	smtp, xerr := r.GetSMTPCredentials(ctx, f.account)
	if xerr != nil || smtp.SMTPPassword != "smtp-pass" || smtp.IMAPHost != "imap.x.test" {
		t.Fatalf("SMTP credentials read back as %+v, %v", smtp, xerr)
	}
	creds, xerr := r.GetOAuthCredentials(ctx, f.account)
	if xerr != nil || creds.AccessToken != "access" || creds.RefreshToken != "refresh" {
		t.Fatalf("OAuth credentials read back as %+v, %v", creds, xerr)
	}
}
