package form

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/owndomain"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

type ownMailboxes []string

func (o *ownMailboxes) OwnedDomains(context.Context, uuid.UUID) ([]string, *errx.Error) {
	return *o, nil
}

// oneForm serves a single form; every other repository call is unused here.
type oneForm struct {
	repository.FormRepository
	form *models.Form
}

func (r *oneForm) Get(context.Context, uuid.UUID, uuid.UUID) (*models.Form, *errx.Error) {
	f := *r.form
	return &f, nil
}

func (r *oneForm) GetByPublicID(context.Context, string) (*models.Form, *errx.Error) {
	f := *r.form
	return &f, nil
}

func (r *oneForm) Update(_ context.Context, _ uuid.UUID, f *models.Form) (*models.Form, *errx.Error) {
	r.form = f
	out := *f
	return &out, nil
}

func TestFormRedirectGoesOnlyToTheWorkspacesOwnDomains(t *testing.T) {
	ctx := context.Background()
	org := uuid.New()
	own := &ownMailboxes{"acme.com"}
	repo := &oneForm{form: &models.Form{ID: uuid.New(), OrganizationID: org, Status: models.FormStatusPublished, SuccessMessage: "Thanks"}}
	s := NewService(repo).(*service)
	s.SetOwnDomains(owndomain.New(own, nil))

	for _, bad := range []string{"https://evil-acme.com/", "https://acme.com.evil.io/"} {
		u := bad
		if _, xerr := s.Update(ctx, org, repo.form.ID, &models.FormWrite{RedirectURL: &u}); xerr == nil || xerr.Identifier != owndomain.ErrIDNotOwn {
			t.Errorf("%q = %v", bad, xerr)
		}
	}
	good := "https://www.acme.com/thanks"
	f, xerr := s.Update(ctx, org, repo.form.ID, &models.FormWrite{RedirectURL: &good})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if f.RedirectURLActive == nil || !*f.RedirectURLActive {
		t.Fatalf("an own redirect is not active: %+v", f.RedirectURLActive)
	}
	// A trapped submit answers like a real one, so it shows what a visitor gets.
	res, xerr := s.Submit(ctx, "pub", nil, SubmitMeta{HoneypotFilled: true})
	if xerr != nil || res.RedirectURL != good {
		t.Fatalf("Submit = %+v, %v", res, xerr)
	}

	// The workspace no longer has a mailbox on acme.com: visitors get the thank-you message.
	*own = nil
	if res, _ = s.Submit(ctx, "pub", nil, SubmitMeta{HoneypotFilled: true}); res.RedirectURL != "" || res.Message != "Thanks" {
		t.Fatalf("a redirect off the workspace's domains was followed: %+v", res)
	}
	if f, _ = s.Get(ctx, org, repo.form.ID); f.RedirectURLActive == nil || *f.RedirectURLActive {
		t.Fatalf("an inactive redirect is not flagged: %+v", f.RedirectURLActive)
	}
	// Saving the form's other settings keeps the stored destination instead of refusing the save.
	name := "Renamed"
	if _, xerr := s.Update(ctx, org, repo.form.ID, &models.FormWrite{Name: &name, RedirectURL: &good}); xerr != nil {
		t.Fatalf("an unchanged destination blocked a save: %v", xerr)
	}
	empty := ""
	if f, xerr = s.Update(ctx, org, repo.form.ID, &models.FormWrite{RedirectURL: &empty}); xerr != nil || f.RedirectURL != "" {
		t.Fatalf("clearing the redirect = %v", xerr)
	}
}
