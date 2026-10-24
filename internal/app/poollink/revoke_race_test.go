package poollink

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/app/email"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// raceRepo holds several enrollments and refuses an insert on a revoked link, as the SQL does.
type raceRepo struct {
	*managedOpsRepo
	data       sync.Mutex
	mailboxes  map[uuid.UUID]models.PoolLinkMailbox
	contending chan struct{}
	inserted   chan struct{}
}

// WithManagedOperationLock announces a caller that has to wait for the lock.
func (r *raceRepo) WithManagedOperationLock(_ context.Context, _ uuid.UUID, fn func() error) error {
	if !r.mu.TryLock() {
		r.data.Lock()
		if r.contending != nil {
			close(r.contending)
			r.contending = nil
		}
		r.data.Unlock()
		r.mu.Lock()
	}
	defer r.mu.Unlock()
	return fn()
}

func (r *raceRepo) revoked() bool {
	r.data.Lock()
	defer r.data.Unlock()
	return r.instance.RevokedAt != nil
}

func (r *raceRepo) RevokeInstance(context.Context, uuid.UUID) error {
	r.data.Lock()
	defer r.data.Unlock()
	now := time.Now()
	r.instance.RevokedAt = &now
	return nil
}

func (r *raceRepo) EnrollMailbox(_ context.Context, m *models.PoolLinkMailbox) error {
	r.data.Lock()
	defer r.data.Unlock()
	if r.inserted != nil {
		defer close(r.inserted)
		r.inserted = nil
	}
	if r.instance.RevokedAt != nil {
		return repository.ErrPoolLinkInstanceRevoked
	}
	r.mailboxes[m.RemoteID] = *m
	return nil
}

func (r *raceRepo) GetMailboxByRemote(_ context.Context, instance, remote uuid.UUID) (*models.PoolLinkMailbox, error) {
	r.data.Lock()
	defer r.data.Unlock()
	if m, ok := r.mailboxes[remote]; ok && m.InstanceID == instance {
		return &m, nil
	}
	return nil, nil
}

func (r *raceRepo) ListMailboxes(context.Context, uuid.UUID) ([]models.PoolLinkMailbox, error) {
	r.data.Lock()
	defer r.data.Unlock()
	out := make([]models.PoolLinkMailbox, 0, len(r.mailboxes))
	for _, m := range r.mailboxes {
		out = append(out, m)
	}
	return out, nil
}

func (r *raceRepo) DeleteMailbox(_ context.Context, _, remote uuid.UUID) error {
	r.data.Lock()
	defer r.data.Unlock()
	delete(r.mailboxes, remote)
	return nil
}

// raceEmails records each mailbox's warmup participation; the first stop runs onStop.
type raceEmails struct {
	email.EmailService
	mu      sync.Mutex
	warming map[uuid.UUID]bool
	deleted map[uuid.UUID]bool
	onStop  func()
}

func (e *raceEmails) SetWarmupLifecycle(_ context.Context, _, id, action string) (*models.Email, *errx.Error) {
	e.mu.Lock()
	e.warming[uuid.MustParse(id)] = action == "start" || action == "resume"
	hook := e.onStop
	if action == "stop" {
		e.onStop = nil
	}
	e.mu.Unlock()
	if action == "stop" && hook != nil {
		hook()
	}
	return &models.Email{}, nil
}

func (e *raceEmails) Delete(_ context.Context, _, id string) *errx.Error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.deleted[uuid.MustParse(id)] = true
	return nil
}

func (*raceEmails) LoadAccountOntoWorker(context.Context, uuid.UUID) error { return nil }

func TestEnrollmentRacingRevokeIsStoppedOrRefused(t *testing.T) {
	t.Setenv("BILLING_PROVIDER", "none")
	org, owner, instanceID := uuid.New(), uuid.New(), uuid.New()
	inst := &models.PoolLinkInstance{ID: instanceID, OrganizationID: org, CreatedBy: &owner}
	existing := models.PoolLinkMailbox{InstanceID: instanceID, RemoteID: uuid.New(), EmailAccountID: uuid.New(), Managed: true}
	r := &raceRepo{
		managedOpsRepo: &managedOpsRepo{instance: &models.PoolLinkInstance{ID: instanceID, OrganizationID: org, CreatedBy: &owner}},
		mailboxes:      map[uuid.UUID]models.PoolLinkMailbox{existing.RemoteID: existing},
		contending:     make(chan struct{}),
		inserted:       make(chan struct{}),
	}
	accounts := &existingConnectionAccounts{managedOpsEmails: managedOpsEmails{accounts: map[uuid.UUID]*models.Email{}}}
	emails := &raceEmails{warming: map[uuid.UUID]bool{existing.EmailAccountID: true}, deleted: map[uuid.UUID]bool{}}
	s := &service{repo: r, emails: accounts, emailSvc: emails}

	tls := &models.Service{Host: "mail.example.test", Port: 993, Security: models.MailSecurityTLS, Username: "late@example.test", Password: "synthetic"}
	req := models.PoolLinkEnrollRequest{RemoteID: uuid.New(), Email: "late@example.test", Provider: models.InboxProviderSMTPIMAP, SMTPIMAP: &models.SmtpImap{SMTP: tls, IMAP: tls}}
	var enrollErr *errx.Error
	var wg sync.WaitGroup
	contending, inserted := r.contending, r.inserted
	// The instance passed authentication before the revoke and enrolls while it runs.
	emails.onStop = func() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, enrollErr = s.Enroll(context.Background(), inst, req)
		}()
		select {
		case <-contending:
		case <-inserted:
		case <-time.After(5 * time.Second):
			t.Error("the racing enrollment neither inserted nor waited for the instance lock")
		}
	}

	if xerr := s.RevokeInstance(context.Background(), org, instanceID); xerr != nil {
		t.Fatal(xerr)
	}
	wg.Wait()

	if !r.revoked() {
		t.Fatal("the link was not revoked")
	}
	if enrollErr != ErrInstanceRevoked {
		t.Fatalf("racing enrollment = %v, want it refused as revoked", enrollErr)
	}
	if left, _ := r.ListMailboxes(context.Background(), instanceID); len(left) != 0 {
		t.Fatalf("enrollments left on a revoked link: %+v", left)
	}
	emails.mu.Lock()
	defer emails.mu.Unlock()
	for id, on := range emails.warming {
		if on {
			t.Fatalf("mailbox %v is warming after its link was revoked", id)
		}
	}
	for id := range accounts.accounts {
		if !emails.deleted[id] {
			t.Fatalf("the refused enrollment's mailbox %v was left behind", id)
		}
	}
}
