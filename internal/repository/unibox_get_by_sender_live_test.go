package repository

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

// from_addr keeps the display name ("Name <addr>" or "Name (addr)"), so a
// lookup by the bare address has to match on the address part.
// WARMBLY_TEST_DB=postgres://... go test ./internal/repository/ -run LiveUniboxGetBySender -v
func TestLiveUniboxGetBySenderMatchesBareAddress(t *testing.T) {
	handle := liveUniboxFolderDB(t)
	f := newUniboxFolderFixture(t, handle.Pool)
	repo := NewUniboxRepository(handle)
	ctx := context.Background()

	from := func(addr string) uuid.UUID {
		id := f.message(t, repo, models.FolderInbox)
		if _, err := handle.Pool.Exec(ctx, "UPDATE unibox_emails SET from_addr = ARRAY[$2::text] WHERE id = $1", id, addr); err != nil {
			t.Fatalf("set from_addr: %v", err)
		}
		return id
	}
	angle := from("Jane Doe <Jane@Example.com>")
	paren := from("Centous Support (support@centous.com)")
	comment := from("Sam Roe <sam@example.net> (work)")
	quotedName := from(`"VIP <alice@example.com>" <mallory@example.net>`)
	bareComment := from("eve@example.net (bob@example.com)")

	cases := []struct {
		sender string
		want   uuid.UUID
	}{
		{"jane@example.com", angle},
		{" Jane@Example.com ", angle},
		{"Jane Doe <Jane@Example.com>", angle},
		{"support@centous.com", paren},
		{"sam@example.net", comment},
		{"work", uuid.Nil},
		// The address is the sender's, never one written into the name or a comment.
		{"mallory@example.net", quotedName},
		{"alice@example.com", uuid.Nil},
		{"eve@example.net", bareComment},
		{"bob@example.com", uuid.Nil},
		{"jane@example.org", uuid.Nil},
	}
	for _, c := range cases {
		got, err := repo.GetBySender(ctx, f.org, c.sender, 10, "")
		if err != nil {
			t.Fatalf("GetBySender(%q): %v", c.sender, err)
		}
		var ids []uuid.UUID
		for _, m := range got.Data {
			ids = append(ids, m.ID)
		}
		if c.want == uuid.Nil && len(ids) != 0 || c.want != uuid.Nil && (len(ids) != 1 || ids[0] != c.want) {
			t.Errorf("GetBySender(%q) = %v, want %v", c.sender, ids, c.want)
		}
	}
}

// Mail is an organization asset: a reply that reached a teammate's mailbox is
// found, and the same sender writing to another workspace is not.
func TestLiveUniboxGetBySenderIsScopedToTheOrganization(t *testing.T) {
	handle := liveUniboxFolderDB(t)
	repo := NewUniboxRepository(handle)
	ctx := context.Background()

	f := newUniboxFolderFixture(t, handle.Pool)
	teammate := newUniboxFolderFixture(t, handle.Pool)
	if _, err := handle.Pool.Exec(ctx, "UPDATE email_accounts SET organization_id = $2 WHERE id = $1", teammate.mailbox, f.org); err != nil {
		t.Fatalf("move teammate mailbox: %v", err)
	}
	stranger := newUniboxFolderFixture(t, handle.Pool)

	own := f.message(t, repo, models.FolderInbox)
	shared := teammate.message(t, repo, models.FolderInbox)
	foreign := stranger.message(t, repo, models.FolderInbox)

	collect := func(org uuid.UUID) map[uuid.UUID]bool {
		t.Helper()
		seen := map[uuid.UUID]bool{}
		cursor := ""
		for range 5 {
			page, err := repo.GetBySender(ctx, org, "support@centous.com", 1, cursor)
			if err != nil {
				t.Fatalf("GetBySender: %v", err)
			}
			for _, m := range page.Data {
				seen[m.ID] = true
			}
			if !page.Pagination.HasMore || page.Pagination.NextCursor == nil {
				break
			}
			cursor = *page.Pagination.NextCursor
		}
		return seen
	}

	got := collect(f.org)
	if len(got) != 2 || !got[own] || !got[shared] || got[foreign] {
		t.Errorf("workspace lookup = %v, want own %s and teammate's %s only", got, own, shared)
	}
	if got := collect(stranger.org); len(got) != 1 || !got[foreign] {
		t.Errorf("other workspace lookup = %v, want only %s", got, foreign)
	}
}
