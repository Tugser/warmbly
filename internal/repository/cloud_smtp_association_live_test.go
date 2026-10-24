package repository

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
)

func TestLiveSMTPAssociationIntentRetainsSelectedCloudIDAcrossOldWritersAndRestart(t *testing.T) {
	f := newPoolLinkFixture(t)
	requireSchemaVersion(t, f.pool, 322)
	ctx := t.Context()
	instance, cloud := uuid.New(), uuid.New()
	if _, err := f.pool.Exec(ctx, `INSERT INTO cloud_link(cloud_url,instance_id,token,organization_id) VALUES('https://cloud.test',$1,'fixture',$2)`, instance, f.org); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM cloud_link WHERE instance_id=$1`, instance)
	})
	r := NewCloudLinkRepository(f.pool, nil, testSessionConnector(f.pool))
	var firstRemote uuid.UUID
	for index := range 2 {
		if err := r.(CloudSMTPAssociationRepository).BeginSMTPAssociation(ctx, f.sender, instance, cloud); err != nil {
			t.Fatal(err)
		}
		intent, err := r.GetByAccount(ctx, f.sender)
		if err != nil || intent == nil {
			t.Fatalf("saved intent: %+v %v", intent, err)
		}
		if index == 0 {
			firstRemote = intent.RemoteID
		} else if intent.RemoteID != firstRemote {
			t.Fatal("retry changed the saved remote operation identity")
		}
	}
	if err := r.(CloudSMTPAssociationRepository).BeginSMTPAssociation(ctx, f.sender, instance, uuid.New()); err == nil {
		t.Fatal("association selected a different Cloud mailbox")
	}
	// The previous release writes these same enrollment fields and leaves the added identifier intact.
	if err := r.BeginEnrollment(ctx, f.sender, f.sender, instance); err != nil {
		t.Fatal(err)
	}
	intent, err := r.GetByAccount(ctx, f.sender)
	if err != nil || intent == nil {
		t.Fatalf("intent: %+v %v", intent, err)
	}
	if intent.RemoteID != firstRemote {
		t.Fatal("an older enrollment writer changed the saved remote identity")
	}
	if _, err := r.Enroll(ctx, f.sender, intent.RemoteID, instance, false); err != nil {
		t.Fatal(err)
	}
	got, err := NewCloudLinkRepository(f.pool, nil, testSessionConnector(f.pool)).GetByAccount(ctx, f.sender)
	if err != nil || got == nil || got.SMTPCloudAccountID == nil || *got.SMTPCloudAccountID != cloud || got.Managed || got.EnrollmentState != "active" {
		t.Fatalf("durable association: %+v %v", got, err)
	}
	var previousID uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT email_account_id FROM cloud_link_mailboxes WHERE email_account_id=$1 AND NOT managed`, f.sender).Scan(&previousID); err != nil || previousID != f.sender {
		t.Fatalf("previous reader: %v", err)
	}
	down, err := os.ReadFile("../infrastructure/db/migrations/000322_cloud_smtp_association.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, string(down)); err == nil {
		t.Error("schema downgrade discarded a live association")
	}
	_ = tx.Rollback(ctx)
}
