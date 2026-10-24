package poollink

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/app/email"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

type detachedRepo struct {
	repository.PoolLinkRepository
	state     repository.DetachedWarmupStop
	mailboxes []repository.DetachedWarmupMailbox
	listed    int
}

func (r *detachedRepo) DetachedWarmupStopState(_ context.Context, now time.Time) (*repository.DetachedWarmupStop, error) {
	if r.state.FencedAt.IsZero() {
		r.state.FencedAt = now
	}
	st := r.state
	return &st, nil
}

func (r *detachedRepo) MarkDetachedWarmupStopDone(_ context.Context, at time.Time) error {
	r.state.DoneAt = &at
	return nil
}

func (r *detachedRepo) ListDetachedWarmupMailboxes(context.Context) ([]repository.DetachedWarmupMailbox, error) {
	r.listed++
	return r.mailboxes, nil
}

type detachedEmails struct {
	email.EmailService
	stops []uuid.UUID
	fail  map[uuid.UUID]*errx.Error
}

func (e *detachedEmails) SetWarmupLifecycle(_ context.Context, _, id, action string) (*models.Email, *errx.Error) {
	if action != "stop" {
		panic("the pass must stop both directions")
	}
	account := uuid.MustParse(id)
	if xerr := e.fail[account]; xerr != nil {
		return nil, xerr
	}
	e.stops = append(e.stops, account)
	return &models.Email{}, nil
}

func TestStopDetachedWarmupRunsUntilACompletePassAfterTheRollout(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	r := &detachedRepo{mailboxes: []repository.DetachedWarmupMailbox{{EmailAccountID: a, OrganizationID: uuid.New()}, {EmailAccountID: b, OrganizationID: uuid.New()}}}
	e := &detachedEmails{fail: map[uuid.UUID]*errx.Error{b: errx.InternalError()}}
	s := &service{repo: r, emailSvc: e}

	if err := s.StopDetachedWarmup(context.Background()); err == nil || r.state.DoneAt != nil {
		t.Fatal("a pass with a failed stop must report it and stay incomplete", err)
	}
	e.fail = nil
	if err := s.StopDetachedWarmup(context.Background()); err != nil || r.state.DoneAt != nil {
		t.Fatal("a pass inside the rollout window must not complete the stop", err)
	}
	if len(e.stops) != 3 {
		t.Fatalf("stops = %v, want a, then a and b", e.stops)
	}

	r.state.FencedAt = time.Now().Add(-DetachedStopRollout - time.Minute)
	if err := s.StopDetachedWarmup(context.Background()); err != nil || r.state.DoneAt == nil {
		t.Fatal("a complete pass after the rollout window must mark the stop done", err)
	}

	// An owner restarting warmup on Cloud afterwards is not stopped again.
	stops, listed := len(e.stops), r.listed
	if err := s.StopDetachedWarmup(context.Background()); err != nil || len(e.stops) != stops || r.listed != listed {
		t.Fatal("a completed stop ran again", err)
	}
}

func TestStopDetachedWarmupIgnoresAMailboxAlreadyGone(t *testing.T) {
	gone := uuid.New()
	r := &detachedRepo{
		state:     repository.DetachedWarmupStop{FencedAt: time.Now().Add(-time.Hour)},
		mailboxes: []repository.DetachedWarmupMailbox{{EmailAccountID: gone, OrganizationID: uuid.New()}},
	}
	s := &service{repo: r, emailSvc: &detachedEmails{fail: map[uuid.UUID]*errx.Error{gone: errx.ErrNotFound}}}
	if err := s.StopDetachedWarmup(context.Background()); err != nil || r.state.DoneAt == nil {
		t.Fatal("a mailbox deleted since must not hold the stop open", err)
	}
}
