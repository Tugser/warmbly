package repository

import (
	"testing"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

func TestLiveMailboxGetCarriesOrganizationForWarmupEdits(t *testing.T) {
	handle, pool := liveContactDB(t)
	f := newWarmupUsageFixture(t, pool)
	r := NewEmailRepostory(handle, nil)

	account, xerr := r.Get(t.Context(), f.org.String(), f.account.String())
	if xerr != nil {
		t.Fatal(xerr)
	}
	if account.OrganizationID == nil || *account.OrganizationID != f.org {
		t.Fatalf("mailbox organization = %v, want %s", account.OrganizationID, f.org)
	}

	base := 4
	updated, xerr := r.Update(t.Context(), account.OrganizationID.String(), account.ID.String(), &models.UpdateEmail{WarmupBase: &base})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if updated.WarmupBase != base {
		t.Fatalf("starting volume = %d, want %d", updated.WarmupBase, base)
	}

	account, xerr = r.Get(t.Context(), f.org.String(), f.account.String())
	if xerr != nil || account == nil || account.WarmupBase != base {
		t.Fatalf("saved mailbox = %+v, error = %v", account, xerr)
	}
	if _, xerr := r.Get(t.Context(), uuid.NewString(), f.account.String()); xerr != errx.ErrNotFound {
		t.Fatalf("other workspace lookup = %v, want not found", xerr)
	}
}
