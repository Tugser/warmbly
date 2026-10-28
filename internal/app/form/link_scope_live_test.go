package form

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/infrastructure/db"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// A form link token counts only on the form it was minted for, and only for a contact of that form's workspace.
func TestLiveFormLinkTokenIsBoundToItsForm(t *testing.T) {
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
	ownerA, ownerB, orgA, orgB, contactA, contactB := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, u := range []uuid.UUID{ownerA, ownerB} {
		exec(`INSERT INTO users (id, email, first_name, last_name) VALUES ($1, $2, 'Form', 'Link')`, u, u.String()+"@test.local")
	}
	t.Cleanup(func() {
		exec(`DELETE FROM forms WHERE organization_id = ANY($1)`, []uuid.UUID{orgA, orgB})
		exec(`DELETE FROM organizations WHERE id = ANY($1)`, []uuid.UUID{orgA, orgB})
		exec(`DELETE FROM users WHERE id = ANY($1)`, []uuid.UUID{ownerA, ownerB})
	})
	for _, o := range []struct{ org, owner, contact uuid.UUID }{{orgA, ownerA, contactA}, {orgB, ownerB, contactB}} {
		exec(`INSERT INTO organizations (id, name, slug, owner_user_id) VALUES ($1, 'Forms', $2, $3)`, o.org, o.org.String(), o.owner)
		exec(`INSERT INTO organization_members (organization_id, user_id, role, permissions, accepted_at) VALUES ($1, $2, 'owner', -1, NOW())`, o.org, o.owner)
		exec(`INSERT INTO contacts (id, user_id, organization_id, email, first_name, last_name, company, phone, custom_fields) VALUES ($1, $2, $3, $4, 'Prefill', 'Me', '', '', '{}')`, o.contact, o.owner, o.org, o.contact.String()+"@test.local")
	}

	forms := repository.NewFormRepository(handle)
	links := repository.NewFormLinkRepository(handle)
	svc := NewService(forms)
	svc.SetLinks(links)
	svc.SetEvents(repository.NewFormEventRepository(handle))
	svc.SetContactReader(repository.NewContactRepostory(handle))
	publish := func(org, owner uuid.UUID) *models.Form {
		t.Helper()
		f, xerr := forms.Create(ctx, org, &owner, &models.Form{
			PublicID:       "linkscope-" + uuid.New().String()[:13],
			Name:           "Link scope",
			Status:         models.FormStatusPublished,
			Fields:         []models.FormField{{ID: "first", Type: models.FormFieldText, Label: "First name", MapTo: "first_name"}},
			SuccessMessage: "Thanks",
		})
		if xerr != nil {
			t.Fatal(xerr)
		}
		return f
	}
	formX, formY, formZ := publish(orgA, ownerA), publish(orgA, ownerA), publish(orgB, ownerB)

	token, xerr := links.Upsert(ctx, orgA, formX.ID, contactA, nil)
	if xerr != nil {
		t.Fatal(xerr)
	}
	tok := token.String()

	t.Run("token resolves with prefill on its own form", func(t *testing.T) {
		link, prefill := svc.ResolveLink(ctx, formX, tok)
		if link == nil || link.ContactID != contactA || prefill["first"] != "Prefill" {
			t.Fatalf("link = %+v, prefill = %v", link, prefill)
		}
	})
	for _, other := range []struct {
		name string
		form *models.Form
	}{{"another form in the same workspace", formY}, {"another workspace's form", formZ}} {
		t.Run("token resolves to nothing on "+other.name, func(t *testing.T) {
			if link, prefill := svc.ResolveLink(ctx, other.form, tok); link != nil || prefill != nil {
				t.Fatalf("link = %+v, prefill = %v", link, prefill)
			}
		})
		t.Run("event on "+other.name+" carries no contact", func(t *testing.T) {
			if xerr := svc.RecordEvent(ctx, other.form.PublicID, EventInput{Type: string(models.FormEventView), LinkToken: tok}); xerr != nil {
				t.Fatal(xerr)
			}
			var attributed int
			if err := handle.QueryRow(ctx, `SELECT COUNT(*) FROM form_events WHERE form_id = $1 AND contact_id IS NOT NULL`, other.form.ID).Scan(&attributed); err != nil {
				t.Fatal(err)
			}
			if attributed != 0 {
				t.Fatal("a token from another form attributed an event")
			}
		})
		t.Run("submission on "+other.name+" carries no contact", func(t *testing.T) {
			if _, xerr := svc.Submit(ctx, other.form.PublicID, map[string][]string{"first": {"Visitor"}}, SubmitMeta{LinkToken: tok}); xerr != nil {
				t.Fatal(xerr)
			}
			var total, attributed int
			if err := handle.QueryRow(ctx, `SELECT COUNT(*), COUNT(contact_id) FROM form_submissions WHERE form_id = $1`, other.form.ID).Scan(&total, &attributed); err != nil {
				t.Fatal(err)
			}
			if total != 1 || attributed != 0 {
				t.Fatalf("submissions = %d, attributed = %d", total, attributed)
			}
		})
	}
	t.Run("a link cannot be minted for another workspace's contact", func(t *testing.T) {
		if _, xerr := links.Upsert(ctx, orgA, formX.ID, contactB, nil); xerr == nil {
			t.Fatal("linked a contact from another workspace")
		}
	})
	t.Run("a link cannot be minted on another workspace's form", func(t *testing.T) {
		if _, xerr := svc.MintLink(ctx, orgA, formZ.ID, contactA); xerr == nil {
			t.Fatal("minted a link on another workspace's form")
		}
		if _, _, ok := svc.MintForContact(ctx, orgA, formZ.PublicID, contactA, nil); ok {
			t.Fatal("minted a campaign link on another workspace's form")
		}
	})
}
