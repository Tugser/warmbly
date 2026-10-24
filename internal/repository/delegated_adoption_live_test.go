package repository

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/infrastructure/db"
	"github.com/warmbly/warmbly/internal/models"
)

func TestLiveDelegatedGoogleAdoptionPreservesCloudIdentityAndHistory(t *testing.T) {
	f := newPoolLinkFixture(t)
	ctx := t.Context()
	r := NewPoolLinkRepository(f.pool, testSessionConnector(f.pool))
	inst := &models.PoolLinkInstance{ID: uuid.New(), OrganizationID: f.org}
	if err := r.CreateInstance(ctx, inst, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC().Add(-28 * 24 * time.Hour).Truncate(time.Second)
	if _, err := f.pool.Exec(ctx, `UPDATE email_accounts SET provider='gmail',auth_method='delegated',warmup=$2,warmup_max=42 WHERE id=$1`, f.sender, started); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE email_accounts SET provider='outlook',auth_method='delegated' WHERE id=$1`, f.recipient); err != nil {
		t.Fatal(err)
	}
	list, err := r.ListAdoptableMailboxes(ctx, f.org, false)
	if err != nil || len(list) != 1 || list[0].ID != f.sender {
		t.Fatalf("adoptable: %+v %v", list, err)
	}
	if other, err := r.ListAdoptableMailboxes(ctx, uuid.New(), true); err != nil || len(other) != 0 {
		t.Fatalf("other workspace: %+v %v", other, err)
	}
	mr := r.(PoolLinkManagedRepository)
	remote := uuid.New()
	op := &models.PoolLinkManagedOperation{OrganizationID: f.org, InstanceID: &inst.ID, RemoteID: &remote,
		Kind: "adopt", Provider: models.InboxProviderGoogle, PlannedAccountID: &f.sender, ExpiresAt: time.Now().Add(time.Minute)}
	if err := mr.CreateManagedOperation(ctx, op); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := mr.CompleteManagedOperation(ctx, inst.ID, remote, f.sender); err != nil {
			t.Fatal(err)
		}
	}
	if list, err := r.ListAdoptableMailboxes(ctx, f.org, false); err != nil || len(list) != 0 {
		t.Fatalf("already associated: %+v %v", list, err)
	}
	var gotStart time.Time
	var auth string
	var max, tasks int
	if err := f.pool.QueryRow(ctx, `SELECT warmup,auth_method,warmup_max,(SELECT count(*) FROM tasks WHERE id=$2 AND email_account_id=$1) FROM email_accounts WHERE id=$1`, f.sender, f.task).Scan(&gotStart, &auth, &max, &tasks); err != nil {
		t.Fatal(err)
	}
	if !gotStart.Equal(started) || auth != "delegated" || max != 42 || tasks != 1 {
		t.Fatalf("Cloud mailbox: start=%s want=%s auth=%s max=%d tasks=%d", gotStart, started, auth, max, tasks)
	}
	if err := mr.RevokeManagedOperations(ctx, inst.ID, &remote); err != nil {
		t.Fatal(err)
	}
	if err := mr.CompleteManagedOperation(ctx, inst.ID, remote, f.sender); err == nil {
		t.Fatal("revoked adoption completed")
	}
}

func TestLiveManagedAdoptionConvertsOnlyItsConfirmedLocalMailbox(t *testing.T) {
	for _, refusal := range []string{"", "removing", "active_enrollment", "wrong_address", "wrong_workspace", "revoked", "restricted_workspace", "completion_failure"} {
		t.Run("case_"+refusal, func(t *testing.T) {
			f := newPoolLinkFixture(t)
			ctx := t.Context()
			instance, remote, cloud := uuid.New(), uuid.New(), uuid.New()
			if _, err := f.pool.Exec(ctx, `INSERT INTO cloud_link(cloud_url,instance_id,token,organization_id) VALUES('https://cloud.test',$1,'fixture',$2)`, instance, f.org); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_, _ = f.pool.Exec(context.Background(), `DELETE FROM cloud_link WHERE instance_id=$1`, instance)
			})
			r := NewCloudLinkRepository(f.pool, nil, testSessionConnector(f.pool))
			cr := r.(CloudManagedConsentRepository)
			c := &models.CloudManagedConsent{ID: uuid.New(), OrganizationID: f.org, UserID: &f.user, InstanceID: &instance,
				RemoteID: &remote, CloudAccountID: &cloud, PlannedAccountID: &f.sender, Kind: "adopt", Provider: models.InboxProviderGoogle, ExpiresAt: time.Now().Add(time.Minute)}
			if err := cr.CreateManagedConsent(ctx, c); err != nil {
				t.Fatal(err)
			}
			if err := r.BeginEnrollment(ctx, f.sender, f.sender, instance); err != nil {
				t.Fatal(err)
			}
			if _, err := f.pool.Exec(ctx, `INSERT INTO email_accounts_smtp_imap(email_account_id,smtp_host,smtp_port,smtp_user,smtp_password,imap_host,imap_port,imap_user,imap_password) VALUES($1,'smtp.test',587,'user','fixture','imap.test',993,'user','fixture')`, f.sender); err != nil {
				t.Fatal(err)
			}
			before, xerr := NewEmailRepostory(&db.DB{Pool: f.pool}, nil).GetByID(ctx, f.sender)
			if xerr != nil {
				t.Fatal(xerr)
			}
			address := strings.ToUpper(f.senderTo)
			switch refusal {
			case "removing", "active_enrollment":
				state := "pending_remove"
				if refusal == "active_enrollment" {
					state = "active"
				}
				if _, err := f.pool.Exec(ctx, `UPDATE cloud_link_mailboxes SET enrollment_state=$2 WHERE email_account_id=$1`, f.sender, state); err != nil {
					t.Fatal(err)
				}
			case "wrong_address":
				address = "another@example.test"
			case "wrong_workspace":
				c.OrganizationID = uuid.New()
			case "revoked":
				if err := cr.RevokeManagedConsents(ctx, instance, &f.sender); err != nil {
					t.Fatal(err)
				}
			case "restricted_workspace":
				if _, err := f.pool.Exec(ctx, `UPDATE organizations SET risk_state='restricted' WHERE id=$1`, f.org); err != nil {
					t.Fatal(err)
				}
			case "completion_failure":
				constraint := pgx.Identifier{"adoption_completion_" + strings.ReplaceAll(c.ID.String(), "-", "")}.Sanitize()
				if _, err := f.pool.Exec(ctx, `ALTER TABLE cloud_managed_consents ADD CONSTRAINT `+constraint+` CHECK(id <> '`+c.ID.String()+`'::uuid OR consent_state <> 'active')`); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if _, err := f.pool.Exec(context.Background(), `ALTER TABLE cloud_managed_consents DROP CONSTRAINT `+constraint); err != nil {
						t.Error(err)
					}
				})
			}
			err := r.(CloudManagedMailboxAdopter).ConvertManagedMailbox(ctx, c, address)
			if (err != nil) != (refusal != "") {
				t.Fatalf("conversion: %v", err)
			}
			var provider, auth string
			var generation uuid.UUID
			var creds, tasks int
			if err := f.pool.QueryRow(ctx, `SELECT provider,auth_method,transport_generation,
 (SELECT count(*) FROM email_accounts_smtp_imap WHERE email_account_id=$1),
 (SELECT count(*) FROM tasks WHERE id=$2 AND email_account_id=$1) FROM email_accounts WHERE id=$1`, f.sender, f.task).Scan(&provider, &auth, &generation, &creds, &tasks); err != nil {
				t.Fatal(err)
			}
			if refusal == "" {
				m, err := r.GetByAccount(ctx, f.sender)
				if err != nil || m == nil || !m.Managed || m.RemoteID != remote || m.InstanceID != instance || provider != "gmail" || auth != "oauth" || creds != 0 || generation == before.TransportGeneration || tasks != 1 {
					t.Fatalf("converted mailbox: %+v %v provider=%s auth=%s creds=%d tasks=%d", m, err, provider, auth, creds, tasks)
				}
				consent, err := cr.FindManagedAdoption(ctx, f.org, f.user, instance, cloud)
				if err != nil || consent == nil || consent.State != "active" || consent.AccountID == nil || *consent.AccountID != f.sender {
					t.Fatalf("conversion did not atomically complete consent: %+v %v", consent, err)
				}
			} else if provider != "smtp_imap" || creds != 1 || generation != before.TransportGeneration || tasks != 1 {
				t.Fatal("refused conversion changed mailbox data")
			}
		})
	}
}

func TestLiveManagedMailboxCreationRefusesExistingAddress(t *testing.T) {
	f := newPoolLinkFixture(t)
	r := NewEmailRepostory(&db.DB{Pool: f.pool}, nil)
	if _, xerr := r.NewManagedAccount(t.Context(), f.user.String(), models.NewOauthAccount{OrganizationID: &f.org,
		Provider: models.InboxProviderGoogle, Email: strings.ToUpper(f.senderTo), Name: "Existing"}); xerr != errx.ErrEmailOnboardAlreadyExists {
		t.Fatalf("duplicate managed mailbox: %v", xerr)
	}
}

func TestLiveCloudAppPasswordPickerIsOptInForNewClients(t *testing.T) {
	f := newPoolLinkFixture(t)
	r := NewPoolLinkRepository(f.pool, testSessionConnector(f.pool))
	if _, err := f.pool.Exec(t.Context(), `UPDATE email_accounts SET mail_host='google_workspace' WHERE id=$1`, f.sender); err != nil {
		t.Fatal(err)
	}
	legacy, err := r.ListAdoptableMailboxes(t.Context(), f.org, false)
	if err != nil || len(legacy) != 0 {
		t.Fatalf("legacy client received a new mailbox type: %+v %v", legacy, err)
	}
	list, err := r.ListAdoptableMailboxes(t.Context(), f.org, true)
	if err != nil || len(list) != 2 {
		t.Fatalf("opted-in list: %+v %v", list, err)
	}
	for _, mailbox := range list {
		if mailbox.ID == f.sender && mailbox.MailHost != "google_workspace" {
			t.Fatal("Google app-password mailbox lost its provider label")
		}
	}
}
