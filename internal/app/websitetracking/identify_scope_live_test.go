package websitetracking

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/infrastructure/db"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// A click ticket identifies a visitor only on its own workspace's site, and page hits are read only within their workspace.
func TestLiveIdentifyTicketStaysInItsWorkspace(t *testing.T) {
	dsn := os.Getenv("WARMBLY_TEST_DB")
	if dsn == "" {
		t.Skip("WARMBLY_TEST_DB not set")
	}
	ctx := context.Background()
	handle, err := db.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(handle.Pool.Close)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := handle.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	type workspace struct {
		org, owner, contact, mailbox, campaign, task, ticket uuid.UUID
		siteKey, host                                        string
	}
	newWorkspace := func(host string) workspace {
		return workspace{uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), "site-" + strings.ReplaceAll(uuid.NewString(), "-", ""), host}
	}
	a, b := newWorkspace("a-site.example"), newWorkspace("b-site.example")
	t.Cleanup(func() {
		exec(`DELETE FROM tasks WHERE id = ANY($1)`, []uuid.UUID{a.task, b.task})
		exec(`DELETE FROM organizations WHERE id = ANY($1)`, []uuid.UUID{a.org, b.org})
		exec(`DELETE FROM users WHERE id = ANY($1)`, []uuid.UUID{a.owner, b.owner})
	})
	for _, w := range []workspace{a, b} {
		exec(`INSERT INTO users (id, email, first_name, last_name) VALUES ($1, $2, 'Site', 'Owner')`, w.owner, w.owner.String()+"@test.local")
		exec(`INSERT INTO organizations (id, name, slug, owner_user_id) VALUES ($1, 'Site', $2, $3)`, w.org, w.org.String(), w.owner)
		exec(`INSERT INTO contacts (id, user_id, organization_id, email, first_name, last_name, company, phone, custom_fields) VALUES ($1, $2, $3, $4, 'Lead', 'L', '', '', '{}')`, w.contact, w.owner, w.org, w.contact.String()+"@test.local")
		exec(`INSERT INTO email_accounts (id, user_id, organization_id, email, name, signature_plain, signature_html, provider) VALUES ($1, $2, $3, $4, 'Sender', '', '', 'smtp_imap')`, w.mailbox, w.owner, w.org, w.mailbox.String()+"@test.local")
		exec(`INSERT INTO campaigns (id, user_id, organization_id, name, description, days, updated_at, created_at) VALUES ($1, $2, $3, 'C', '', 62, NOW(), NOW())`, w.campaign, w.owner, w.org)
		exec(`INSERT INTO tasks (id, task_type, email_account_id, status, message_id) VALUES ($1, 'campaign', $2, 'completed', '')`, w.task, w.mailbox)
		exec(`INSERT INTO campaign_tasks (task_id, campaign_id, contact_id) VALUES ($1, $2, $3)`, w.task, w.campaign, w.contact)
		exec(`INSERT INTO tracked_links (id, task_id, campaign_id, destination) VALUES ($1, $2, $3, $4)`, w.ticket, w.task, w.campaign, "https://"+w.host+"/pricing")
		exec(`INSERT INTO website_tracking_settings (organization_id, enabled, site_key, consent_mode, allowed_hosts) VALUES ($1, true, $2, 'implicit', $3)`, w.org, w.siteKey, []string{w.host})
	}

	repo := repository.NewWebsiteTrackingRepository(handle.Pool)
	svc := NewService(repo, nil, nil)
	hit := func(t *testing.T, site workspace, visitorKey, ticket string) *models.WebsiteHitResult {
		t.Helper()
		res, err := svc.Ingest(ctx, &models.WebsiteHitRequest{
			SiteKey: site.siteKey, VisitorKey: visitorKey, SessionKey: "session-" + strings.ReplaceAll(uuid.NewString(), "-", ""),
			Consent: "implicit", IdentifyToken: ticket, URL: "https://" + site.host + "/pricing?wbly_t=" + ticket,
			UserAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15",
		})
		if err != nil {
			t.Fatalf("ingest: %v", err)
		}
		return res
	}
	contactOf := func(t *testing.T, org uuid.UUID, visitorKey string) *uuid.UUID {
		t.Helper()
		var c *uuid.UUID
		if err := handle.QueryRow(ctx, `SELECT contact_id FROM website_visitors WHERE organization_id = $1 AND visitor_key = $2`, org, visitorKey).Scan(&c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	key := func() string { return "visitor-" + strings.ReplaceAll(uuid.NewString(), "-", "") }

	t.Run("A's ticket identifies a visitor on A's site", func(t *testing.T) {
		v := key()
		hit(t, a, v, a.ticket.String())
		if c := contactOf(t, a.org, v); c == nil || *c != a.contact {
			t.Fatalf("visitor contact = %v, want A's contact", c)
		}
	})
	t.Run("A's ticket on B's site identifies nobody", func(t *testing.T) {
		v := key()
		if res := hit(t, b, v, a.ticket.String()); res != nil && res.NewVisitorKey != "" {
			t.Fatal("B's site was handed a visitor key for A's contact")
		}
		if c := contactOf(t, b.org, v); c != nil {
			t.Fatalf("B's visitor was tied to contact %s", c)
		}
	})
	t.Run("a visitor key identified in A is anonymous in B", func(t *testing.T) {
		v := key()
		hit(t, a, v, a.ticket.String())
		hit(t, b, v, "")
		if c := contactOf(t, b.org, v); c != nil {
			t.Fatalf("B's visitor inherited contact %s", c)
		}
	})
	t.Run("B's workspace stores no row naming A's contact", func(t *testing.T) {
		var n int
		if err := handle.QueryRow(ctx, `SELECT COUNT(*) FROM website_visitors WHERE organization_id = $1 AND contact_id = $2`, b.org, a.contact).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("%d of B's visitors name A's contact", n)
		}
	})
	t.Run("A's contact's hits are read only within A", func(t *testing.T) {
		own, err := repo.ListHitsForContact(ctx, a.org, a.contact, time.Now().Add(time.Minute), 50)
		if err != nil || len(own) == 0 {
			t.Fatalf("A's own hits = %d, %v", len(own), err)
		}
		foreign, err := repo.ListHitsForContact(ctx, b.org, a.contact, time.Now().Add(time.Minute), 50)
		if err != nil || len(foreign) != 0 {
			t.Fatalf("B read %d of A's contact's hits (%v)", len(foreign), err)
		}
	})
	t.Run("B's campaign never carries a ticket to A's site", func(t *testing.T) {
		if svc.ShouldIdentify(ctx, b.campaign, "https://"+a.host+"/pricing") {
			t.Fatal("B's click would hand its ticket to A's site")
		}
		if !svc.ShouldIdentify(ctx, b.campaign, "https://"+b.host+"/pricing") {
			t.Fatal("B's click should carry its ticket to B's own site")
		}
	})
}
