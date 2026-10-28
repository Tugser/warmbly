package poollink

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/warmbly/warmbly/internal/app/cipher"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/events"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

var execNow = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

func arrivalFixture() (uuid.UUID, models.PoolLinkWarmupArrival, arrivalEvidence) {
	recipient, sender := uuid.New(), uuid.New()
	token := &models.WarmupToken{Token: uuid.New(), TaskID: uuid.New(), SenderAccountID: sender, RecipientAccountID: recipient,
		SentMessageID: "<minted@cloud.example>", CreatedAt: execNow.Add(-time.Hour), ExpiresAt: execNow.Add(48 * time.Hour)}
	a := models.PoolLinkWarmupArrival{MessageHandle: "h1", MessageID: "minted@cloud.example", From: "Pat <pat@sender.example>",
		Subject: "hello", ReceivedAt: execNow.Add(-30 * time.Minute)}
	return recipient, a, arrivalEvidence{Token: token, TokenPresented: true, SenderEmail: "PAT@sender.example", SentIDs: []string{token.SentMessageID}, Settled: true}
}

func TestCheckArrival(t *testing.T) {
	cases := []struct {
		name    string
		edit    func(account *uuid.UUID, a *models.PoolLinkWarmupArrival, p *arrivalEvidence)
		verdict string
		reason  string
		pending bool
	}{
		{name: "accepted", verdict: models.PoolLinkArrivalAccepted},
		{name: "sender copy", edit: func(acc *uuid.UUID, _ *models.PoolLinkWarmupArrival, p *arrivalEvidence) {
			*acc = p.Token.SenderAccountID
		},
			verdict: models.PoolLinkArrivalRejected, reason: ReasonSentCopy},
		{name: "another mailbox", edit: func(acc *uuid.UUID, _ *models.PoolLinkWarmupArrival, _ *arrivalEvidence) { *acc = uuid.New() },
			verdict: models.PoolLinkArrivalRejected, reason: ReasonTokenOtherMailbox},
		{name: "both ends self-executed", edit: func(_ *uuid.UUID, _ *models.PoolLinkWarmupArrival, p *arrivalEvidence) { p.SenderExecuted = true },
			verdict: models.PoolLinkArrivalRejected, reason: ReasonSelfExecutedPair},
		{name: "sender mismatch", edit: func(_ *uuid.UUID, a *models.PoolLinkWarmupArrival, _ *arrivalEvidence) { a.From = "x@other.example" },
			verdict: models.PoolLinkArrivalRejected, reason: ReasonSenderMismatch},
		{name: "before send", edit: func(_ *uuid.UUID, a *models.PoolLinkWarmupArrival, _ *arrivalEvidence) {
			a.ReceivedAt = execNow.Add(-2 * time.Hour)
		},
			verdict: models.PoolLinkArrivalRejected, reason: ReasonReceivedBeforeSend},
		{name: "future", edit: func(_ *uuid.UUID, a *models.PoolLinkWarmupArrival, _ *arrivalEvidence) {
			a.ReceivedAt = execNow.Add(time.Hour)
		},
			verdict: models.PoolLinkArrivalRejected, reason: ReasonReceivedInFuture},
		{name: "expired", edit: func(_ *uuid.UUID, _ *models.PoolLinkWarmupArrival, p *arrivalEvidence) {
			p.Token.ExpiresAt = execNow.Add(-45 * time.Minute)
		}, verdict: models.PoolLinkArrivalRejected, reason: ReasonTokenExpired},
		{name: "id mismatch", edit: func(_ *uuid.UUID, a *models.PoolLinkWarmupArrival, _ *arrivalEvidence) {
			a.MessageID = "<forged@x.example>"
		},
			verdict: models.PoolLinkArrivalRejected, reason: ReasonMessageIDMismatch},
		{name: "unsettled id waits", edit: func(_ *uuid.UUID, a *models.PoolLinkWarmupArrival, p *arrivalEvidence) {
			a.MessageID, p.Settled = "<restamped@provider.example>", false
		}, pending: true},
		{name: "restamped id recorded", edit: func(_ *uuid.UUID, a *models.PoolLinkWarmupArrival, p *arrivalEvidence) {
			a.MessageID = "<restamped@provider.example>"
			p.SentIDs = append(p.SentIDs, "restamped@provider.example")
		}, verdict: models.PoolLinkArrivalAccepted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			account, a, p := arrivalFixture()
			if tc.edit != nil {
				tc.edit(&account, &a, &p)
			}
			v, pending := checkArrival(account, a, p, execNow)
			if pending != tc.pending {
				t.Fatalf("pending = %v, want %v", pending, tc.pending)
			}
			if tc.pending {
				return
			}
			if v.Verdict != tc.verdict || v.Reason != tc.reason {
				t.Fatalf("verdict = %+v, want %s/%s", v, tc.verdict, tc.reason)
			}
		})
	}
}

func TestArrivalEventCarriesOnlyVerifiedToken(t *testing.T) {
	m := &repository.PoolLinkExecMailbox{EmailAccountID: uuid.New(), UserID: uuid.New(), Email: "me@rcpt.example"}
	token := uuid.New()
	a := models.PoolLinkWarmupArrival{MessageID: "id@x", From: "a@b.example", LandedSpam: true,
		Flags: []string{"category_promotions", "X-Mailtrace-Verify: " + uuid.NewString(), "\\Seen"}, ReceivedAt: execNow}
	ev := arrivalEvent(m, a, arrivalInternalID(m.EmailAccountID, "h"), true, token, execNow)
	msg := ev.Body.(*models.JobEventNewEmail).Message
	if msg.Folder != models.FolderSpam || msg.MessageID != "<id@x>" {
		t.Fatalf("folder %q id %q", msg.Folder, msg.MessageID)
	}
	want := []string{"CATEGORY_PROMOTIONS", "X-Mailtrace-Verify: " + token.String()}
	if strings.Join(msg.Flags, "|") != strings.Join(want, "|") {
		t.Fatalf("flags = %v, want %v", msg.Flags, want)
	}
	if arrivalInternalID(m.EmailAccountID, "h") != msg.ID {
		t.Fatal("internal id is not stable per handle")
	}
}

func storedCommand(kind, status string) *repository.StoredPoolLinkCommand {
	c := &repository.StoredPoolLinkCommand{ID: uuid.New(), Kind: kind, Status: status, EmailAccountID: uuid.New()}
	if kind == models.PoolLinkCommandSend {
		task, nonce := uuid.New(), uuid.New()
		c.TaskID, c.DispatchNonce = &task, &nonce
	}
	return c
}

func TestDecideAnswerSend(t *testing.T) {
	sent := models.PoolLinkCommandResult{Outcome: "sent", MessageID: "<restamped@provider.example>"}
	c := storedCommand(models.PoolLinkCommandSend, "leased")
	a, xerr := decideAnswer(c, nil, sent, execNow)
	if xerr != nil || a == nil || a.SendResult == nil || !a.SendResult.Success || a.SendResult.MessageID != sent.MessageID || a.SendResult.TaskID != *c.TaskID {
		t.Fatalf("sent answer = %+v, %v", a, xerr)
	}

	c.Status, c.Result = "answered", a.Result
	if a, xerr := decideAnswer(c, nil, sent, execNow); a != nil || xerr != nil {
		t.Fatalf("identical replay = %+v, %v", a, xerr)
	}
	if _, xerr := decideAnswer(c, nil, models.PoolLinkCommandResult{Outcome: "failed", ErrorCode: "send_failed"}, execNow); xerr != ErrCommandAnswered {
		t.Fatalf("conflicting answer = %v", xerr)
	}
	if _, xerr := decideAnswer(storedCommand(models.PoolLinkCommandSend, "pending"), nil, sent, execNow); xerr != ErrCommandNotLeased {
		t.Fatalf("unleased = %v", xerr)
	}
	if a, xerr := decideAnswer(storedCommand(models.PoolLinkCommandSend, "expired"), nil, sent, execNow); xerr != nil || a == nil || !a.SendResult.Success {
		t.Fatalf("late send = %+v, %v", a, xerr)
	}
	if a, xerr := decideAnswer(storedCommand(models.PoolLinkCommandSend, "expired"), nil, models.PoolLinkCommandResult{Outcome: "expired"}, execNow); a != nil || xerr != nil {
		t.Fatalf("expired replay = %+v, %v", a, xerr)
	}
	if _, xerr := decideAnswer(storedCommand(models.PoolLinkCommandSend, "cancelled"), nil, sent, execNow); xerr != ErrCommandAnswered {
		t.Fatalf("withdrawn = %v", xerr)
	}
	for _, bad := range []models.PoolLinkCommandResult{{Outcome: "done"}, {Outcome: "sent", MessageID: "no-at-sign"}, {Outcome: "sent", MessageID: "a@b\r\nX: y"}} {
		if _, xerr := decideAnswer(storedCommand(models.PoolLinkCommandSend, "leased"), nil, bad, execNow); xerr != ErrCommandResultInvalid {
			t.Fatalf("%+v = %v", bad, xerr)
		}
	}
}

func TestSendFailuresNeverHoldTheMailbox(t *testing.T) {
	task := uuid.New()
	for _, r := range []models.PoolLinkCommandResult{
		{Outcome: "failed", ErrorCode: "SEND_FAILED", Permanent: true, ErrorMessage: "bad\x00 auth‮"},
		{Outcome: "failed", ErrorCode: "send_released"},
		{Outcome: "refused", ErrorCode: "worker_offline"},
		{Outcome: "expired"},
	} {
		res, xerr := sendResult(task, normalizeResult(r), execNow)
		if xerr != nil || res.Success || res.Error == nil || res.Error.Failure == nil {
			t.Fatalf("%+v = %+v, %v", r, res, xerr)
		}
		f := res.Error.Failure
		if f.Disposition != errx.SendRetry {
			t.Fatalf("%+v disposition = %q", r, f.Disposition)
		}
		walkedBack := r.Outcome != "failed"
		if walkedBack != (f.Protocol == "internal" && f.Stage == "prepare" && f.RetryAt == nil) {
			t.Fatalf("%+v walk-back = %+v", r, f)
		}
		if r.Permanent && (f.RetryAt == nil || !f.RetryAt.Equal(execNow.Add(time.Hour))) {
			t.Fatalf("permanent cooldown = %v", f.RetryAt)
		}
		if strings.ContainsAny(res.Error.Message, "\x00‮") {
			t.Fatalf("message not sanitized: %q", res.Error.Message)
		}
	}
}

func TestActionAnswersBecomeWorkerEvents(t *testing.T) {
	internal, filing := uuid.New(), uuid.New()
	origin := &models.WarmupEmailAction{UserID: uuid.New(), EmailID: uuid.New(), RFCMessageID: "<m@x>", InternalID: internal.String(), FilingID: filing.String(),
		Actions: []string{models.WarmupActionRescueFromSpam, models.WarmupActionVerifyRemoval}, Recheck: true}
	done := actionEvents(origin, models.PoolLinkCommandResult{Outcome: "done", Rescue: "moved", Removal: "present"})
	if len(done) != 3 {
		t.Fatalf("done events = %d", len(done))
	}
	if r := done[0].Body.(*models.JobEventWarmupRescued); r.Outcome != models.WarmupRescueMoved || r.InternalID != internal {
		t.Fatalf("rescue = %+v", r)
	}
	if r := done[1].Body.(*models.JobEventWarmupRemovalChecked); r.Outcome != models.WarmupRemovalPresent || !r.Recheck {
		t.Fatalf("removal = %+v", r)
	}
	if f := done[2].Body.(*models.JobEventWarmupFiled); f.FilingID != filing {
		t.Fatalf("filed = %+v", f)
	}
	expired := actionEvents(origin, models.PoolLinkCommandResult{Outcome: "expired", Rescue: "moved", Removal: "trashed"})
	if len(expired) != 2 || expired[0].Body.(*models.JobEventWarmupRescued).Outcome != models.WarmupRescueFailed ||
		expired[1].Body.(*models.JobEventWarmupRemovalChecked).Outcome != models.WarmupRemovalUnknown {
		t.Fatalf("expired events = %+v", expired)
	}
	c := storedCommand(models.PoolLinkCommandAction, "expired")
	if _, xerr := decideAnswer(c, &commandEnvelope{Origin: origin}, models.PoolLinkCommandResult{Outcome: "done", Rescue: "moved"}, execNow); xerr != ErrCommandAnswered {
		t.Fatalf("late action = %v", xerr)
	}
}

func TestNextReporterTrust(t *testing.T) {
	sample := func(spam int, expected float64) repository.ReporterTrustSample {
		return repository.ReporterTrustSample{Reported: 100, ReportedSpam: spam, Observed: 200, ExpectedSpam: expected}
	}
	cases := []struct {
		current string
		s       repository.ReporterTrustSample
		want    string
	}{
		{"", repository.ReporterTrustSample{Reported: 5, Observed: 500}, models.PoolLinkReportProbation},
		{models.PoolLinkReportTrusted, repository.ReporterTrustSample{Reported: 500, Observed: 5}, models.PoolLinkReportTrusted},
		{"", sample(10, 10), models.PoolLinkReportTrusted},
		{models.PoolLinkReportTrusted, sample(30, 10), models.PoolLinkReportProbation},
		{models.PoolLinkReportProbation, sample(45, 10), models.PoolLinkReportUntrusted},
		{models.PoolLinkReportUntrusted, sample(30, 10), models.PoolLinkReportUntrusted},
		{models.PoolLinkReportUntrusted, sample(15, 10), models.PoolLinkReportTrusted},
		{models.PoolLinkReportTrusted, sample(0, 40), models.PoolLinkReportUntrusted},
	}
	for _, tc := range cases {
		if got := nextReporterTrust(tc.current, tc.s); got != tc.want {
			t.Fatalf("nextReporterTrust(%q, %+v) = %q, want %q", tc.current, tc.s, got, tc.want)
		}
	}
}

type execRepoFake struct {
	repository.PoolLinkExecRepository
	executed *repository.PoolLinkExecMailbox
	handle   string
	queued   []repository.QueuedPoolLinkCommand
}

func (f *execRepoFake) InstanceExecuted(context.Context, uuid.UUID) (*repository.PoolLinkExecMailbox, error) {
	return f.executed, nil
}

func (f *execRepoFake) ArrivalHandle(context.Context, uuid.UUID, *uuid.UUID, string) (string, error) {
	return f.handle, nil
}

func (f *execRepoFake) EnqueueCommand(_ context.Context, q repository.QueuedPoolLinkCommand) (bool, error) {
	f.queued = append(f.queued, q)
	return true, nil
}

type innerPublisherFake struct {
	events.Publisher
	sends, actions int
}

func (p *innerPublisherFake) PublishSendEmail(context.Context, uuid.UUID, *events.SendEmailParams) error {
	p.sends++
	return nil
}

func (p *innerPublisherFake) PublishWarmupAction(context.Context, uuid.UUID, *models.WarmupEmailAction) error {
	p.actions++
	return nil
}

func TestSelfExecutionPublisherRouting(t *testing.T) {
	ctx := context.Background()
	org := uuid.New()
	repo := &execRepoFake{executed: &repository.PoolLinkExecMailbox{InstanceID: uuid.New(), EmailAccountID: uuid.New(), OrganizationID: org}}
	inner := &innerPublisherFake{}
	keys := cipher.NewStatic(bytes.Repeat([]byte{7}, 32))
	p := &selfExecPublisher{Publisher: inner, repo: repo, cipher: keys, now: func() time.Time { return execNow }}

	params := &events.SendEmailParams{TaskID: uuid.New(), EmailID: repo.executed.EmailAccountID, OrgID: org, To: []string{"r@x.example"},
		DispatchNonce: uuid.NewString(), MessageID: "<m@cloud>", Subject: "secret subject", BodyPlain: "secret body", IsWarmup: true, WarmupToken: uuid.NewString()}
	if err := p.PublishSendEmail(ctx, uuid.New(), params); err != nil || inner.sends != 1 || len(repo.queued) != 0 {
		t.Fatalf("worker send not passed through: %v", err)
	}
	if err := p.PublishSendEmail(ctx, uuid.Nil, params); err != nil || len(repo.queued) != 1 {
		t.Fatalf("self-executed send not queued: %v", err)
	}
	q := repo.queued[0]
	if q.Kind != models.PoolLinkCommandSend || *q.TaskID != params.TaskID || q.InstanceID != repo.executed.InstanceID || !q.NotAfter.After(execNow) {
		t.Fatalf("queued = %+v", q)
	}
	if strings.Contains(q.Payload, "secret") {
		t.Fatal("payload stored in the clear")
	}
	env, err := openEnvelope(ctx, keys, org, q.Payload)
	if err != nil || env.Send == nil || env.Send.Subject != "secret subject" || env.Send.MessageID != params.MessageID {
		t.Fatalf("payload = %+v, %v", env, err)
	}

	campaign := *params
	campaign.IsWarmup = false
	if err := p.PublishSendEmail(ctx, uuid.Nil, &campaign); !errors.Is(err, errNoExecutor) {
		t.Fatalf("campaign send on the nil worker = %v", err)
	}
	if err := p.PublishMessageSeen(ctx, uuid.Nil, &models.MessageSeenAction{}); !errors.Is(err, errNoExecutor) {
		t.Fatalf("unibox relay on the nil worker = %v", err)
	}

	repo.handle = "inst-handle"
	filing := uuid.NewString()
	action := &models.WarmupEmailAction{EmailID: repo.executed.EmailAccountID, RFCMessageID: "<m@x>", Actions: []string{models.WarmupActionFile}, FilingID: filing}
	if err := p.PublishWarmupAction(ctx, uuid.Nil, action); err != nil || len(repo.queued) != 2 {
		t.Fatalf("action not queued: %v", err)
	}
	if q := repo.queued[1]; q.Kind != models.PoolLinkCommandAction || q.DedupeKey != "filing:"+filing || q.TaskID != nil {
		t.Fatalf("action queued = %+v", q)
	}
	env, err = openEnvelope(ctx, keys, org, repo.queued[1].Payload)
	if err != nil || env.Action.MessageHandle != "inst-handle" || env.Origin.FilingID != filing {
		t.Fatalf("action payload = %+v, %v", env, err)
	}

	repo.executed = nil
	if err := p.PublishWarmupAction(ctx, uuid.Nil, action); !errors.Is(err, errNoExecutor) {
		t.Fatalf("action for a mailbox nobody executes = %v", err)
	}
}

type grantFake struct{ grant *models.SelfExecutionGrant }

func (g grantFake) GetSelfExecutionGrant(context.Context, uuid.UUID) (*models.SelfExecutionGrant, error) {
	return g.grant, nil
}

func TestEnrollExecutorGates(t *testing.T) {
	ctx := context.Background()
	inst := &models.PoolLinkInstance{ID: uuid.New(), OrganizationID: uuid.New()}
	expired := execNow.Add(-time.Minute)
	s := &service{exec: &selfExecution{SelfExecutionDeps: SelfExecutionDeps{Grants: grantFake{&models.SelfExecutionGrant{ExpiresAt: &expired}}, Clock: func() time.Time { return execNow }}}}
	smtp := &models.SmtpImap{SMTP: &models.Service{}, IMAP: &models.Service{}}
	instanceRun := &models.PoolLinkMailbox{Executor: models.PoolLinkExecutorInstance}

	cases := []struct {
		name     string
		existing *models.PoolLinkMailbox
		req      models.PoolLinkEnrollRequest
		want     *errx.Error
	}{
		{"unknown executor", nil, models.PoolLinkEnrollRequest{Executor: "edge"}, ErrExecutorInvalid},
		{"credential with instance", nil, models.PoolLinkEnrollRequest{Executor: models.PoolLinkExecutorInstance, Provider: models.InboxProviderSMTPIMAP, SMTPIMAP: smtp}, ErrExecutorCredential},
		{"expired grant", nil, models.PoolLinkEnrollRequest{Executor: models.PoolLinkExecutorInstance, Provider: models.InboxProviderSMTPIMAP}, ErrSelfExecutionNotGranted},
		{"old instance sends a credential", instanceRun, models.PoolLinkEnrollRequest{SMTPIMAP: smtp}, ErrExecutorConflict},
		{"explicit instance with a credential", instanceRun, models.PoolLinkEnrollRequest{Executor: models.PoolLinkExecutorInstance, SMTPIMAP: smtp}, ErrExecutorCredential},
		{"managed mailbox", &models.PoolLinkMailbox{Managed: true}, models.PoolLinkEnrollRequest{Executor: models.PoolLinkExecutorInstance}, ErrExecutorManaged},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, xerr, routed := s.enrollExecutor(ctx, inst, tc.existing, tc.req)
			if !routed || xerr != tc.want {
				t.Fatalf("routed %v, err %v, want %v", routed, xerr, tc.want)
			}
		})
	}
	if _, _, routed := s.enrollExecutor(ctx, inst, nil, models.PoolLinkEnrollRequest{}); routed {
		t.Fatal("an enrollment naming no executor must keep the Cloud path")
	}
	if _, _, routed := s.enrollExecutor(ctx, inst, &models.PoolLinkMailbox{}, models.PoolLinkEnrollRequest{SMTPIMAP: smtp}); routed {
		t.Fatal("a Cloud-run re-enroll must keep refreshing its credential")
	}

	out, xerr := s.LeaseCommands(ctx, inst, models.PoolLinkLeaseRequest{Limit: 10})
	if xerr != nil || len(out.Commands) != 0 {
		t.Fatalf("lease without a grant = %+v, %v", out, xerr)
	}
	if info := s.selfExecutionInfo(ctx, inst.OrganizationID); info.Protocol != models.PoolLinkSelfExecutionProtocol || info.Granted {
		t.Fatalf("info = %+v", info)
	}
}

func TestArrivalEvidenceVouchesForTheMintedID(t *testing.T) {
	_, a, base := arrivalFixture()
	a.MessageID = "<minted@cloud.example>"
	minted := "minted@cloud.example"
	unsent := *base.Token
	unsent.SentMessageID = ""
	sender := &models.Email{Email: base.SenderEmail}
	cases := []struct {
		name    string
		token   *models.WarmupToken
		send    repository.WarmupTaskSend
		accept  bool
		pending bool
	}{
		{"cloud worker took it", &unsent, repository.WarmupTaskSend{MintedID: minted, CloudExecuted: true}, true, false},
		{"sent result recorded", base.Token, repository.WarmupTaskSend{MintedID: minted, SentRecorded: true, Settled: true}, true, false},
		{"self-executed, nothing recorded yet", &unsent, repository.WarmupTaskSend{MintedID: minted}, false, true},
		{"self-executed, failed", &unsent, repository.WarmupTaskSend{MintedID: minted, Settled: true}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := arrivalEvidenceFor(tc.token, true, sender, tc.send)
			v, pending := checkArrival(tc.token.RecipientAccountID, a, p, execNow)
			if pending != tc.pending {
				t.Fatalf("pending = %v, want %v", pending, tc.pending)
			}
			if !tc.pending && (v.Verdict == models.PoolLinkArrivalAccepted) != tc.accept {
				t.Fatalf("verdict = %+v, accept %v", v, tc.accept)
			}
		})
	}
	p := arrivalEvidenceFor(base.Token, true, sender, repository.WarmupTaskSend{MintedID: minted, CloudExecuted: true, SenderExecuted: true})
	if v, _ := checkArrival(base.Token.RecipientAccountID, a, p, execNow); v.Reason != ReasonSelfExecutedPair {
		t.Fatalf("self-executed sender = %+v", v)
	}
	if p := arrivalEvidenceFor(base.Token, true, nil, repository.WarmupTaskSend{}); p.SenderEmail != "" || len(p.SentIDs) != 1 {
		t.Fatalf("evidence without a sender = %+v", p)
	}
}

func TestTransientValidation(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{errors.New("organization sending suspended"), false},
		{errx.ErrNotFound, false},
		{errx.InternalError(), true},
		{context.DeadlineExceeded, true},
		{fmt.Errorf("load task: %w", &pgconn.PgError{Code: "57P01"}), true},
	}
	for _, tc := range cases {
		if got := transientValidation(tc.err); got != tc.want {
			t.Fatalf("transientValidation(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

type outboundFake struct{ err error }

func (o outboundFake) ValidateOutboundExecution(context.Context, uuid.UUID) error { return o.err }

type leaseRepoFake struct {
	execRepoFake
	released, cancelled, withdrawn int
}

func (f *leaseRepoFake) ReleaseCommandLease(context.Context, uuid.UUID) error {
	f.released++
	return nil
}

func (f *leaseRepoFake) CancelCommand(context.Context, uuid.UUID) error {
	f.cancelled++
	return nil
}

func (f *leaseRepoFake) CancelSelfExecutedSend(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
	f.withdrawn++
	return nil
}

func TestLeasedSendKeepsItsReservationWhenTheCheckCannotRun(t *testing.T) {
	ctx := context.Background()
	org := uuid.New()
	keys := cipher.NewStatic(bytes.Repeat([]byte{7}, 32))
	payload, err := sealEnvelope(ctx, keys, org, commandEnvelope{Send: &models.PoolLinkSendCommand{}})
	if err != nil {
		t.Fatal(err)
	}
	task, nonce := uuid.New(), uuid.New()
	cmd := &repository.StoredPoolLinkCommand{ID: uuid.New(), OrganizationID: org, Kind: models.PoolLinkCommandSend, TaskID: &task, DispatchNonce: &nonce, Payload: payload}

	repo := &leaseRepoFake{}
	s := &service{exec: &selfExecution{SelfExecutionDeps: SelfExecutionDeps{Cipher: keys, Outbound: outboundFake{errx.InternalError()}}, repo: repo}}
	if s.leased(ctx, cmd) != nil || repo.released != 1 || repo.withdrawn != 0 || repo.cancelled != 0 {
		t.Fatalf("transient check: released %d, withdrawn %d, cancelled %d", repo.released, repo.withdrawn, repo.cancelled)
	}

	repo = &leaseRepoFake{}
	s.exec.repo = repo
	s.exec.Outbound = outboundFake{errors.New("organization sending suspended")}
	if s.leased(ctx, cmd) != nil || repo.released != 0 || repo.withdrawn != 1 || repo.cancelled != 1 {
		t.Fatalf("refusal: released %d, withdrawn %d, cancelled %d", repo.released, repo.withdrawn, repo.cancelled)
	}
}
