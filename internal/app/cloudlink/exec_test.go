package cloudlink

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/events"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

type execRepo struct {
	repository.CloudLinkExecRepository
	stored     map[uuid.UUID]repository.CloudCommand
	tasks      map[uuid.UUID]*repository.CloudCommandTask
	answers    map[uuid.UUID][]models.PoolLinkCommandResult
	dispatched map[uuid.UUID]int
	cancelled  []uuid.UUID
	messages   map[uuid.UUID]*repository.CloudWarmupMessage
	started    map[uuid.UUID]bool
	retried    map[uuid.UUID]int
	begun      map[uuid.UUID]int
	reverted   map[uuid.UUID]int
	ownSends   map[string]bool
	workers    map[uuid.UUID]*uuid.UUID
}

func newExecRepo() *execRepo {
	return &execRepo{stored: map[uuid.UUID]repository.CloudCommand{}, tasks: map[uuid.UUID]*repository.CloudCommandTask{},
		answers: map[uuid.UUID][]models.PoolLinkCommandResult{}, dispatched: map[uuid.UUID]int{}, messages: map[uuid.UUID]*repository.CloudWarmupMessage{}, started: map[uuid.UUID]bool{},
		retried: map[uuid.UUID]int{}, begun: map[uuid.UUID]int{}, reverted: map[uuid.UUID]int{}, ownSends: map[string]bool{}, workers: map[uuid.UUID]*uuid.UUID{}}
}

func (r *execRepo) StoreCommand(_ context.Context, c repository.CloudCommand) (bool, error) {
	if _, ok := r.stored[c.ID]; ok {
		return false, nil
	}
	r.stored[c.ID] = c
	return true, nil
}

func (r *execRepo) MailboxByRemote(context.Context, uuid.UUID, uuid.UUID) (*models.CloudLinkMailbox, error) {
	return nil, nil
}

func (r *execRepo) PrepareSendTask(_ context.Context, commandID, _ uuid.UUID, _ string) (*repository.CloudCommandTask, error) {
	t, ok := r.tasks[commandID]
	if !ok {
		t = &repository.CloudCommandTask{Status: "active"}
		r.tasks[commandID] = t
	}
	cp := *t
	return &cp, nil
}

func (r *execRepo) CancelSendTask(_ context.Context, taskID uuid.UUID) error {
	r.cancelled = append(r.cancelled, taskID)
	return nil
}

func (r *execRepo) SendReservation(_ context.Context, taskID uuid.UUID) (*repository.CloudSendReservation, error) {
	t, ok := r.tasks[taskID]
	if !ok {
		return nil, nil
	}
	return &repository.CloudSendReservation{Worker: r.workers[taskID], Nonce: t.Nonce, Started: r.started[taskID], Released: t.Released}, nil
}

func (r *execRepo) RetryCommand(_ context.Context, commandID uuid.UUID, _ time.Time, _ string) error {
	r.retried[commandID]++
	return nil
}

func (r *execRepo) BeginAction(_ context.Context, commandID uuid.UUID, _ time.Time) (bool, error) {
	r.begun[commandID]++
	return r.begun[commandID]-r.reverted[commandID] == 1, nil
}

func (r *execRepo) RevertAction(_ context.Context, commandID uuid.UUID) error {
	r.reverted[commandID]++
	return nil
}

func (r *execRepo) IsOwnCloudSend(_ context.Context, _ uuid.UUID, messageID string) (bool, error) {
	return r.ownSends[messageID], nil
}

func (r *execRepo) MarkDispatched(_ context.Context, commandID uuid.UUID, _ time.Time) error {
	r.dispatched[commandID]++
	return nil
}

func (r *execRepo) AnswerCommand(_ context.Context, commandID uuid.UUID, result models.PoolLinkCommandResult) error {
	r.answers[commandID] = append(r.answers[commandID], result)
	return nil
}

func (r *execRepo) WarmupMessageByHandle(_ context.Context, _ uuid.UUID, handle uuid.UUID) (*repository.CloudWarmupMessage, error) {
	return r.messages[handle], nil
}

type linkRepo struct {
	repository.CloudLinkRepository
	enrollment *models.CloudLinkMailbox
}

func (r *linkRepo) GetByAccount(context.Context, uuid.UUID) (*models.CloudLinkMailbox, error) {
	return r.enrollment, nil
}

type execAccountsFake struct{ acc *models.Email }

func (a execAccountsFake) GetByID(context.Context, uuid.UUID) (*models.Email, *errx.Error) {
	return a.acc, nil
}

type execPublisherFake struct {
	sends   []*events.SendEmailParams
	actions []*models.WarmupEmailAction
	fail    int
}

func (p *execPublisherFake) PublishSendEmail(_ context.Context, _ uuid.UUID, params *events.SendEmailParams) error {
	p.sends = append(p.sends, params)
	if p.fail > 0 {
		p.fail--
		return errors.New("bus unavailable")
	}
	return nil
}

func (p *execPublisherFake) PublishWarmupAction(_ context.Context, _ uuid.UUID, action *models.WarmupEmailAction) error {
	p.actions = append(p.actions, action)
	if p.fail > 0 {
		p.fail--
		return errors.New("bus unavailable")
	}
	return nil
}

// admissionFake writes the nonce onto the task the way ReserveOutbound does.
type admissionFake struct {
	repo       *execRepo
	reserved   int
	deny       error
	recipients []string
	cancelled  []uuid.UUID
}

func (a *admissionFake) ReserveOutbound(_ context.Context, in repository.OutboundReservation) (uuid.UUID, error) {
	a.reserved++
	a.recipients = in.Recipients
	if a.deny != nil {
		return uuid.Nil, a.deny
	}
	n, w := uuid.New(), in.WorkerID
	a.repo.tasks[in.TaskID].Nonce, a.repo.workers[in.TaskID] = &n, &w
	a.repo.tasks[in.TaskID].Status = "completed"
	return n, nil
}

func (a *admissionFake) CancelOutbound(_ context.Context, task, _, _, _ uuid.UUID) error {
	a.cancelled = append(a.cancelled, task)
	if !a.repo.started[task] {
		a.repo.tasks[task].Released = true
	}
	return nil
}

type liveFake struct{}

func (liveFake) IsWorkerLive(context.Context, uuid.UUID) (bool, error) { return true, nil }

type sealerFake struct{}

func (sealerFake) Seal(_ context.Context, _ uuid.UUID, plain string) (string, error) {
	return "sealed:" + base64.StdEncoding.EncodeToString([]byte(plain)), nil
}

func (sealerFake) Open(_ context.Context, _ uuid.UUID, sealed string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(sealed, "sealed:"))
	return string(b), err
}

type execFixture struct {
	x         *Execution
	repo      *execRepo
	links     *linkRepo
	pub       *execPublisherFake
	admission *admissionFake
	acc       *models.Email
	instance  uuid.UUID
	now       time.Time
}

func newExecFixture() *execFixture {
	org, worker, instance := uuid.New(), uuid.New(), uuid.New()
	acc := &models.Email{ID: uuid.New(), UserID: uuid.NewString(), OrganizationID: &org, WorkerID: &worker,
		Email: "owner@example.com", Name: "Owner", Provider: "smtp_imap", Status: "active"}
	repo := newExecRepo()
	links := &linkRepo{enrollment: &models.CloudLinkMailbox{EmailAccountID: acc.ID, InstanceID: instance, RemoteID: acc.ID,
		Executor: models.PoolLinkExecutorInstance, EnrollmentState: "active"}}
	pub := &execPublisherFake{}
	adm := &admissionFake{repo: repo}
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	x := &Execution{svc: &service{repo: links}, repo: repo, accounts: execAccountsFake{acc}, publisher: pub, admission: adm,
		liveness: liveFake{}, sealer: sealerFake{}, now: func() time.Time { return now }}
	return &execFixture{x: x, repo: repo, links: links, pub: pub, admission: adm, acc: acc, instance: instance, now: now}
}

func (f *execFixture) sendCommand(notAfter time.Time) repository.CloudCommand {
	cmd := models.PoolLinkCommand{ID: uuid.New(), RemoteID: f.acc.ID, Kind: models.PoolLinkCommandSend, NotAfter: notAfter,
		Send: &models.PoolLinkSendCommand{To: "partner@example.org", Subject: "Quick question", BodyPlain: "Hello there",
			MessageID: "<abc@cloud.example>", WarmupToken: uuid.NewString()}}
	raw, _ := json.Marshal(cmd)
	sealed, _ := sealerFake{}.Seal(context.Background(), uuid.Nil, string(raw))
	return repository.CloudCommand{ID: cmd.ID, InstanceID: f.instance, EmailAccountID: f.acc.ID, Kind: cmd.Kind, Payload: sealed, NotAfter: notAfter}
}

func TestExecuteSendReservesOnceAcrossReclaims(t *testing.T) {
	f := newExecFixture()
	f.pub.fail = 1
	c := f.sendCommand(f.now.Add(10 * time.Minute))

	if err := f.x.execute(context.Background(), c); err == nil {
		t.Fatal("a failed publish must leave the command for the next pass")
	}
	if err := f.x.execute(context.Background(), c); err != nil {
		t.Fatalf("re-claimed send: %v", err)
	}
	if f.admission.reserved != 1 {
		t.Fatalf("reserved %d times, want once", f.admission.reserved)
	}
	if len(f.pub.sends) != 2 || f.pub.sends[0].DispatchNonce != f.pub.sends[1].DispatchNonce {
		t.Fatalf("a re-claimed send must republish the same reservation: %+v", f.pub.sends)
	}
	s := f.pub.sends[1]
	if !s.IsWarmup || s.TaskID != c.ID || s.MessageID != "<abc@cloud.example>" || s.FromName != "Owner" {
		t.Fatalf("send params = %+v", s)
	}
	if f.repo.dispatched[c.ID] != 1 || len(f.repo.answers[c.ID]) != 0 {
		t.Fatalf("dispatched %d, answers %v", f.repo.dispatched[c.ID], f.repo.answers[c.ID])
	}
}

func TestExecuteExpiredCommandAnswersExpiredWithoutSending(t *testing.T) {
	f := newExecFixture()
	c := f.sendCommand(f.now)

	if err := f.x.execute(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if got := f.repo.answers[c.ID]; len(got) != 1 || got[0].Outcome != models.PoolLinkOutcomeExpired {
		t.Fatalf("answers = %+v, want one expired", got)
	}
	if f.admission.reserved != 0 || len(f.pub.sends) != 0 {
		t.Fatal("an expired command must not reserve or send")
	}
}

func TestExecuteExpiredSendTheWorkerBeganWaitsForItsResult(t *testing.T) {
	f := newExecFixture()
	c := f.sendCommand(f.now.Add(-time.Second))
	n, w := uuid.New(), uuid.New()
	f.repo.tasks[c.ID] = &repository.CloudCommandTask{Nonce: &n, Status: "completed"}
	f.repo.workers[c.ID] = &w
	f.repo.started[c.ID] = true

	if err := f.x.execute(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if len(f.repo.answers[c.ID]) != 0 || f.repo.dispatched[c.ID] != 1 {
		t.Fatalf("a begun send is answered by its worker result, not expired: answers %+v", f.repo.answers[c.ID])
	}
	if len(f.pub.sends) != 0 {
		t.Fatal("an expired command must not be published again")
	}
}

func TestExecuteRefusesMailboxCloudExecutes(t *testing.T) {
	f := newExecFixture()
	f.links.enrollment.Executor = models.PoolLinkExecutorCloud
	c := f.sendCommand(f.now.Add(time.Minute))

	if err := f.x.execute(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if got := f.repo.answers[c.ID]; len(got) != 1 || got[0].Outcome != models.PoolLinkOutcomeRefused || got[0].ErrorCode != refuseMailboxNotExecuted {
		t.Fatalf("answers = %+v", got)
	}
	if f.admission.reserved != 0 || len(f.pub.sends) != 0 {
		t.Fatal("a mailbox Cloud executes must never send from here")
	}
}

func TestExecuteSendDeniedByLocalAdmissionRetriesBeforeTheDeadline(t *testing.T) {
	f := newExecFixture()
	f.admission.deny = repository.ErrSendAdmissionDenied
	c := f.sendCommand(f.now.Add(time.Minute))

	if err := f.x.execute(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if f.repo.retried[c.ID] != 1 || len(f.repo.answers[c.ID]) != 0 || len(f.repo.cancelled) != 0 {
		t.Fatalf("a hold or full window must retry, not refuse: retried %d answers %+v", f.repo.retried[c.ID], f.repo.answers[c.ID])
	}
	if len(f.pub.sends) != 0 {
		t.Fatal("a denied send must not be published")
	}
}

func TestExecuteSendToSuppressedRecipientIsRefused(t *testing.T) {
	f := newExecFixture()
	f.admission.deny = &repository.RecipientSuppressedError{Address: "partner@example.org"}
	c := f.sendCommand(f.now.Add(time.Minute))

	if err := f.x.execute(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if len(f.repo.cancelled) != 1 || f.repo.cancelled[0] != c.ID {
		t.Fatalf("cancelled = %v", f.repo.cancelled)
	}
	if got := f.repo.answers[c.ID]; len(got) != 1 || got[0].Outcome != models.PoolLinkOutcomeRefused || got[0].ErrorCode != "recipient_suppressed" {
		t.Fatalf("answers = %+v", got)
	}
}

func TestExecuteSendPassesTheBareRecipient(t *testing.T) {
	f := newExecFixture()
	c := f.sendCommand(f.now.Add(time.Minute))
	raw, _ := sealerFake{}.Open(context.Background(), uuid.Nil, c.Payload)
	var cmd models.PoolLinkCommand
	_ = json.Unmarshal([]byte(raw), &cmd)
	cmd.Send.To = "Partner Name <Partner@Example.org>"
	b, _ := json.Marshal(cmd)
	c.Payload, _ = sealerFake{}.Seal(context.Background(), uuid.Nil, string(b))

	if err := f.x.execute(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if len(f.admission.recipients) != 1 || f.admission.recipients[0] != "Partner@Example.org" || f.pub.sends[0].To[0] != "Partner@Example.org" {
		t.Fatalf("recipients %v, to %v", f.admission.recipients, f.pub.sends[0].To)
	}
}

func TestExpiredSendReleasesAnUnstartedReservation(t *testing.T) {
	f := newExecFixture()
	f.pub.fail = 1
	c := f.sendCommand(f.now.Add(time.Minute))
	_ = f.x.execute(context.Background(), c)
	if f.admission.reserved != 1 {
		t.Fatal("expected a reservation before the publish failed")
	}
	f.x.now = func() time.Time { return f.now.Add(2 * time.Minute) }

	if err := f.x.execute(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if len(f.admission.cancelled) != 1 {
		t.Fatal("an unstarted reservation must be released so the mailbox's other sends are not held")
	}
	if got := f.repo.answers[c.ID]; len(got) != 1 || got[0].Outcome != models.PoolLinkOutcomeExpired {
		t.Fatalf("answers = %+v", got)
	}
}

func TestExecuteRefusesMalformedSend(t *testing.T) {
	f := newExecFixture()
	c := f.sendCommand(f.now.Add(time.Minute))
	raw, _ := sealerFake{}.Open(context.Background(), uuid.Nil, c.Payload)
	var cmd models.PoolLinkCommand
	_ = json.Unmarshal([]byte(raw), &cmd)
	cmd.Send.To = "a@example.org, b@example.org"
	b, _ := json.Marshal(cmd)
	c.Payload, _ = sealerFake{}.Seal(context.Background(), uuid.Nil, string(b))

	if err := f.x.execute(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if got := f.repo.answers[c.ID]; len(got) != 1 || got[0].ErrorCode != refuseInvalidCommand {
		t.Fatalf("answers = %+v", got)
	}
	if f.admission.reserved != 0 {
		t.Fatal("a malformed send must not reserve")
	}
}

func TestExecuteActionResolvesHandle(t *testing.T) {
	f := newExecFixture()
	handle, internal := uuid.New(), uuid.New()
	userID := uuid.New()
	f.repo.messages[handle] = &repository.CloudWarmupMessage{Handle: handle, EmailAccountID: f.acc.ID, UserID: userID, InternalID: internal,
		RFCMessageID: "<w@cloud.example>", UID: 42, UIDValidity: 7, FolderPath: "INBOX"}
	cmd := models.PoolLinkCommand{ID: uuid.New(), RemoteID: f.acc.ID, Kind: models.PoolLinkCommandAction, NotAfter: f.now.Add(time.Minute),
		Action: &models.PoolLinkActionCommand{MessageHandle: handle.String(), Actions: []string{models.WarmupActionMarkRead, "format_disk"}}}
	raw, _ := json.Marshal(cmd)
	sealed, _ := sealerFake{}.Seal(context.Background(), uuid.Nil, string(raw))
	c := repository.CloudCommand{ID: cmd.ID, InstanceID: f.instance, EmailAccountID: f.acc.ID, Kind: cmd.Kind, Payload: sealed, NotAfter: cmd.NotAfter}

	if err := f.x.execute(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if len(f.pub.actions) != 1 {
		t.Fatalf("actions = %d", len(f.pub.actions))
	}
	a := f.pub.actions[0]
	if a.UID != 42 || a.MailboxUIDValidity != 7 || a.InternalID != internal.String() || a.UserID != userID || len(a.Actions) != 1 || a.Actions[0] != models.WarmupActionMarkRead {
		t.Fatalf("action = %+v", a)
	}
	if a.CommandID != c.ID.String() || f.repo.begun[c.ID] != 1 {
		t.Fatalf("the action must carry its command and open its window first: %+v", a)
	}
	if got := f.repo.answers[c.ID]; len(got) != 0 {
		t.Fatalf("an action is answered done only once the worker is admitted: %+v", got)
	}
}

func (f *execFixture) actionCommand(a *models.PoolLinkActionCommand) repository.CloudCommand {
	cmd := models.PoolLinkCommand{ID: uuid.New(), RemoteID: f.acc.ID, Kind: models.PoolLinkCommandAction, NotAfter: f.now.Add(time.Minute), Action: a}
	raw, _ := json.Marshal(cmd)
	sealed, _ := sealerFake{}.Seal(context.Background(), uuid.Nil, string(raw))
	return repository.CloudCommand{ID: cmd.ID, InstanceID: f.instance, EmailAccountID: f.acc.ID, Kind: cmd.Kind, Payload: sealed, NotAfter: cmd.NotAfter}
}

func TestExecuteActionWithoutHandleOnlyReachesOwnCloudSends(t *testing.T) {
	f := newExecFixture()
	f.repo.ownSends["<sent@cloud.example>"] = true

	foreign := f.actionCommand(&models.PoolLinkActionCommand{RFCMessageID: "<someone-elses@mail.example>", Actions: []string{models.WarmupActionDelete}})
	if err := f.x.execute(context.Background(), foreign); err != nil {
		t.Fatal(err)
	}
	if got := f.repo.answers[foreign.ID]; len(got) != 1 || got[0].ErrorCode != refuseUnknownHandle {
		t.Fatalf("answers = %+v", got)
	}
	if len(f.pub.actions) != 0 {
		t.Fatal("an action on a message this instance never reported or sent must not reach the worker")
	}

	own := f.actionCommand(&models.PoolLinkActionCommand{RFCMessageID: "<sent@cloud.example>", Actions: []string{models.WarmupActionDelete}})
	if err := f.x.execute(context.Background(), own); err != nil {
		t.Fatal(err)
	}
	if len(f.pub.actions) != 1 || f.pub.actions[0].RFCMessageID != "<sent@cloud.example>" {
		t.Fatalf("actions = %+v", f.pub.actions)
	}
}

func TestExecuteActionPublishFailureClosesItsWindowAndRunsOnce(t *testing.T) {
	f := newExecFixture()
	f.repo.ownSends["<sent@cloud.example>"] = true
	f.pub.fail = 1
	c := f.actionCommand(&models.PoolLinkActionCommand{RFCMessageID: "<sent@cloud.example>", Actions: []string{models.WarmupActionMarkRead}})

	if err := f.x.execute(context.Background(), c); err == nil {
		t.Fatal("a failed publish must leave the command for the next pass")
	}
	if f.repo.reverted[c.ID] != 1 {
		t.Fatal("a failed publish must close the admission window")
	}
	if err := f.x.execute(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	// A pass that finds the window already open publishes nothing.
	if err := f.x.execute(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if len(f.pub.actions) != 2 {
		t.Fatalf("published %d times, want the failed attempt and one retry", len(f.pub.actions))
	}
}

func TestExecuteActionRefusesUnknownHandle(t *testing.T) {
	f := newExecFixture()
	cmd := models.PoolLinkCommand{ID: uuid.New(), RemoteID: f.acc.ID, Kind: models.PoolLinkCommandAction, NotAfter: f.now.Add(time.Minute),
		Action: &models.PoolLinkActionCommand{MessageHandle: uuid.NewString(), Actions: []string{models.WarmupActionStar}}}
	raw, _ := json.Marshal(cmd)
	sealed, _ := sealerFake{}.Seal(context.Background(), uuid.Nil, string(raw))
	c := repository.CloudCommand{ID: cmd.ID, InstanceID: f.instance, EmailAccountID: f.acc.ID, Kind: cmd.Kind, Payload: sealed, NotAfter: cmd.NotAfter}

	if err := f.x.execute(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if got := f.repo.answers[c.ID]; len(got) != 1 || got[0].ErrorCode != refuseUnknownHandle {
		t.Fatalf("answers = %+v", got)
	}
	if len(f.pub.actions) != 0 {
		t.Fatal("an unknown handle must not reach the worker")
	}
}

func TestStoreSealsAndIgnoresReplayedLease(t *testing.T) {
	f := newExecFixture()
	f.x.repo = &storeRepo{execRepo: f.repo, mailbox: f.links.enrollment}
	l := &models.CloudLink{InstanceID: f.instance}
	cmd := models.PoolLinkCommand{ID: uuid.New(), RemoteID: f.acc.ID, Kind: models.PoolLinkCommandSend, NotAfter: f.now.Add(time.Minute),
		Send: &models.PoolLinkSendCommand{To: "p@example.org", Subject: "Lunch", BodyPlain: "a secret body", MessageID: "<m@x>", WarmupToken: "t"}}

	for range 2 {
		if err := f.x.store(context.Background(), l, cmd); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.repo.stored) != 1 {
		t.Fatalf("stored %d commands, want 1", len(f.repo.stored))
	}
	got := f.repo.stored[cmd.ID]
	if strings.Contains(got.Payload, "secret") || strings.Contains(got.Payload, "Lunch") || !strings.HasPrefix(got.Payload, "sealed:") {
		t.Fatalf("payload stored unsealed: %q", got.Payload)
	}
}

type storeRepo struct {
	*execRepo
	mailbox *models.CloudLinkMailbox
}

func (r *storeRepo) MailboxByRemote(context.Context, uuid.UUID, uuid.UUID) (*models.CloudLinkMailbox, error) {
	return r.mailbox, nil
}

func TestSendOutcome(t *testing.T) {
	sentAt := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	ok := sendOutcome(models.SendEmailResult{Success: true, MessageID: "<restamped@outlook>", ThreadID: "th", SentAt: sentAt})
	if ok.Outcome != models.PoolLinkOutcomeSent || ok.MessageID != "<restamped@outlook>" || !ok.SentAt.Equal(sentAt) {
		t.Fatalf("sent = %+v", ok)
	}
	failed := sendOutcome(models.SendEmailResult{Error: &models.EmailSendError{Code: "AUTH_FAILED", UserTitle: strings.Repeat("x", 500),
		Failure: &errx.SendFailure{Disposition: errx.SendAuth}}})
	if failed.Outcome != models.PoolLinkOutcomeFailed || failed.ErrorCode != "AUTH_FAILED" || !failed.Permanent || len(failed.ErrorMessage) != maxErrorMessage {
		t.Fatalf("failed = %+v", failed)
	}
}
