package email

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

type tenantEmailRepo struct {
	repository.EmailRepository
	org, mailbox uuid.UUID
}

func (r *tenantEmailRepo) Get(_ context.Context, orgID, id string) (*models.Email, *errx.Error) {
	if orgID != r.org.String() || id != r.mailbox.String() {
		return nil, errx.ErrNotFound
	}
	return &models.Email{ID: r.mailbox, OrganizationID: &r.org}, nil
}

func (r *tenantEmailRepo) SetWarmupLifecycle(ctx context.Context, orgID, id, _ string) (*models.Email, *errx.Error) {
	return r.Get(ctx, orgID, id)
}

type everyMailboxIsASeed struct{}

func (everyMailboxIsASeed) SeedScope(context.Context, uuid.UUID) (string, error) {
	return "instance", nil
}

// Another workspace's mailbox answers not found, never something about that mailbox.
func TestWarmupStartOnAnotherWorkspacesMailboxIsNotFound(t *testing.T) {
	owner, mailbox := uuid.New(), uuid.New()
	svc := &emailService{emailRepository: &tenantEmailRepo{org: owner, mailbox: mailbox}}
	svc.WireSeedScope(everyMailboxIsASeed{})

	for _, action := range []string{"start", "resume"} {
		_, xerr := svc.SetWarmupLifecycle(context.Background(), uuid.NewString(), mailbox.String(), action)
		if xerr != errx.ErrNotFound {
			t.Fatalf("%s from another workspace = %v, want not found", action, xerr)
		}
	}
}
