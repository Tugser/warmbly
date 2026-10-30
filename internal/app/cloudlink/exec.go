package cloudlink

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/warmbly/warmbly/internal/app/cipher"
	"github.com/warmbly/warmbly/internal/config"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/events"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// Pump pacing. A lease also tells Cloud this instance is alive, so the
// interval stays well inside models.NodeLivenessWindow.
const (
	commandPumpInterval = 30 * time.Second
	commandLeaseLimit   = 50
	commandClaimLease   = 2 * time.Minute
	commandRetryAfter   = time.Minute
	// commandAnswerGrace is how long past NotAfter an action may still report its outcome.
	commandAnswerGrace = 15 * time.Minute
	// ownMoveWindow hides a removal caused by an action this instance just ran.
	ownMoveWindow  = 15 * time.Minute
	reportBatch    = 100
	reportMaxDelay = time.Hour
	commandKeepFor = 7 * 24 * time.Hour
	// messageKeepFor outlasts the longest warmup retention, after which Cloud has had the message deleted.
	messageKeepFor  = (config.RetentionDaysMax + 30) * 24 * time.Hour
	pruneEvery      = time.Hour
	maxErrorMessage = 200
)

// Reasons an instance refuses a command, reported to Cloud as error_code.
const (
	refuseMailboxNotExecuted = "mailbox_not_executed_here"
	refuseMailboxInactive    = "mailbox_inactive"
	refuseWorkerOffline      = "worker_offline"
	refuseInvalidCommand     = "invalid_command"
	refuseUnknownHandle      = "unknown_message_handle"
	refuseNoActions          = "no_actions_permitted"
)

type execAccounts interface {
	GetByID(ctx context.Context, id uuid.UUID) (*models.Email, *errx.Error)
}

type execPublisher interface {
	PublishSendEmail(ctx context.Context, workerID uuid.UUID, params *events.SendEmailParams) error
	PublishWarmupAction(ctx context.Context, workerID uuid.UUID, action *models.WarmupEmailAction) error
}

type execAdmission interface {
	ReserveOutbound(ctx context.Context, in repository.OutboundReservation) (uuid.UUID, error)
	CancelOutbound(ctx context.Context, task, mailbox, worker, nonce uuid.UUID) error
}

type execLiveness interface {
	IsWorkerLive(ctx context.Context, workerID uuid.UUID) (bool, error)
}

// payloadSealer keeps a command's content sealed with the workspace key at rest.
type payloadSealer interface {
	Seal(ctx context.Context, orgID uuid.UUID, plain string) (string, error)
	Open(ctx context.Context, orgID uuid.UUID, sealed string) (string, error)
}

type cipherSealer struct{ c cipher.CipherService }

func (s cipherSealer) Seal(ctx context.Context, orgID uuid.UUID, plain string) (string, error) {
	c, err := s.c.Cipher(ctx, orgID)
	if err != nil {
		return "", err
	}
	return c.Encrypt(ctx, plain)
}

func (s cipherSealer) Open(ctx context.Context, orgID uuid.UUID, sealed string) (string, error) {
	c, err := s.c.Cipher(ctx, orgID)
	if err != nil {
		return "", err
	}
	return c.Decrypt(ctx, sealed)
}

// ExecutionDeps is what the command pump needs from the consumer process.
type ExecutionDeps struct {
	Repo      repository.CloudLinkExecRepository
	Publisher events.Publisher
	Admission repository.OutboundAdmissionRepository
	Liveness  execLiveness
	Cipher    cipher.CipherService
}

// Execution runs Cloud's warmup for mailboxes this instance keeps the only
// credential to: it leases commands, executes them through the instance's
// own worker, reports their outcomes, and reports warmup mail that arrives.
type Execution struct {
	svc       *service
	repo      repository.CloudLinkExecRepository
	accounts  execAccounts
	publisher execPublisher
	admission execAdmission
	liveness  execLiveness
	sealer    payloadSealer
	now       func() time.Time
	lastPrune time.Time
}

// NewExecution returns nil when the service is not this package's own.
func NewExecution(svc Service, deps ExecutionDeps) *Execution {
	s, ok := svc.(*service)
	if !ok || deps.Repo == nil || deps.Publisher == nil || deps.Admission == nil {
		return nil
	}
	s.WireExecution(deps.Repo)
	return &Execution{svc: s, repo: deps.Repo, accounts: s.emails, publisher: deps.Publisher, admission: deps.Admission,
		liveness: deps.Liveness, sealer: cipherSealer{deps.Cipher}, now: time.Now}
}

// Run pumps commands and reports until ctx ends.
func (x *Execution) Run(ctx context.Context) {
	if x == nil {
		return
	}
	ticker := time.NewTicker(commandPumpInterval)
	defer ticker.Stop()
	for {
		x.Pump(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Pump runs one pass: lease, execute, settle, deliver.
func (x *Execution) Pump(ctx context.Context) {
	pass, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	x.leaseAll(pass)
	x.executeDue(pass)
	x.settleAdmitted(pass)
	x.settleAbandoned(pass)
	x.deliverReports(pass)
	if x.now().Sub(x.lastPrune) > pruneEvery {
		if err := x.repo.Prune(pass, commandKeepFor, messageKeepFor); err != nil {
			log.Warn().Err(err).Msg("cloud commands: prune failed")
		}
		x.lastPrune = x.now()
	}
}

func (x *Execution) leaseAll(ctx context.Context) {
	links, err := x.svc.repo.ListLinks(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("cloud commands: links could not be listed")
		return
	}
	for i := range links {
		l := &links[i]
		if l.DisconnectPending {
			continue
		}
		if active, err := x.repo.HasInstanceExecuted(ctx, l.InstanceID); err != nil || !active {
			continue
		}
		info, xerr := x.svc.selfExecution(ctx, l, false)
		if xerr != nil || !selfExecutionSupported(info) {
			continue
		}
		var res models.PoolLinkLeaseResponse
		if xerr := x.svc.clientFor(l).do(ctx, http.MethodPost, "/instance/commands/lease", models.PoolLinkLeaseRequest{Limit: commandLeaseLimit}, &res); xerr != nil {
			log.Warn().Str("instance_id", l.InstanceID.String()).Str("code", xerr.Identifier).Msg("cloud commands: lease failed")
			continue
		}
		for _, cmd := range res.Commands {
			if err := x.store(ctx, l, cmd); err != nil {
				log.Warn().Err(err).Str("command_id", cmd.ID.String()).Msg("cloud commands: command could not be stored")
			}
		}
	}
}

// store keeps a leased command sealed; a replayed lease stores nothing new.
func (x *Execution) store(ctx context.Context, l *models.CloudLink, cmd models.PoolLinkCommand) error {
	if cmd.ID == uuid.Nil {
		return errors.New("command without id")
	}
	m, err := x.repo.MailboxByRemote(ctx, l.InstanceID, cmd.RemoteID)
	if err != nil {
		return err
	}
	if m == nil || m.Executor.OrCloud() != models.PoolLinkExecutorInstance || m.EnrollmentState == "pending_remove" {
		// Nothing to keep it against; Cloud re-leases it if this answer is lost.
		if xerr := x.post(ctx, l, "/instance/commands/"+cmd.ID.String()+"/result", refused(refuseMailboxNotExecuted, "This instance does not run warmup for that mailbox."), nil); xerr != nil && !reportSettled(xerr) {
			return xerr
		}
		return nil
	}
	acc, xerr := x.accounts.GetByID(ctx, m.EmailAccountID)
	if xerr != nil || acc == nil || acc.OrganizationID == nil {
		return errors.New("enrolled mailbox unreadable")
	}
	raw, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	sealed, err := x.sealer.Seal(ctx, *acc.OrganizationID, string(raw))
	if err != nil {
		return err
	}
	stored := repository.CloudCommand{ID: cmd.ID, InstanceID: l.InstanceID, EmailAccountID: m.EmailAccountID, Kind: cmd.Kind, Payload: sealed, NotAfter: cmd.NotAfter}
	if cmd.Kind == models.PoolLinkCommandAction && cmd.Action != nil {
		stored.Actions = cmd.Action.Actions
		stored.RFCMessageID = cmd.Action.RFCMessageID
		if h, err := uuid.Parse(cmd.Action.MessageHandle); err == nil {
			stored.MessageHandle = &h
		}
	}
	if cmd.Kind != models.PoolLinkCommandSend && cmd.Kind != models.PoolLinkCommandAction {
		// Stored so it is answered once, as a kind this build does not run.
		stored.Kind = models.PoolLinkCommandAction
	}
	_, err = x.repo.StoreCommand(ctx, stored)
	return err
}

func (x *Execution) executeDue(ctx context.Context) {
	claimed, err := x.repo.ClaimCommands(ctx, commandLeaseLimit, commandClaimLease)
	if err != nil {
		log.Warn().Err(err).Msg("cloud commands: claim failed")
		return
	}
	for _, c := range claimed {
		if err := x.execute(ctx, c); err != nil {
			log.Warn().Err(err).Str("command_id", c.ID.String()).Msg("cloud commands: execution deferred")
			_ = x.repo.RetryCommand(ctx, c.ID, x.now().Add(commandRetryAfter), err.Error())
		}
	}
}

func refused(code, message string) models.PoolLinkCommandResult {
	return models.PoolLinkCommandResult{Outcome: models.PoolLinkOutcomeRefused, ErrorCode: code, ErrorMessage: message}
}

// execute runs one claimed command; an error leaves it for the next pass.
func (x *Execution) execute(ctx context.Context, c repository.CloudCommand) error {
	if !x.now().Before(c.NotAfter) {
		if c.Kind == models.PoolLinkCommandSend {
			begun, err := x.releaseUnstartedSend(ctx, c)
			if err != nil {
				return err
			}
			if begun {
				// The worker began it before the deadline; its result answers the command.
				return x.repo.MarkDispatched(ctx, c.ID, c.NotAfter)
			}
		}
		return x.repo.AnswerCommand(ctx, c.ID, models.PoolLinkCommandResult{Outcome: models.PoolLinkOutcomeExpired})
	}
	acc, xerr := x.accounts.GetByID(ctx, c.EmailAccountID)
	if xerr != nil {
		if xerr.Code == errx.NotFound {
			return x.repo.AnswerCommand(ctx, c.ID, refused(refuseMailboxNotExecuted, "The mailbox is gone from this instance."))
		}
		return xerr
	}
	if acc.OrganizationID == nil {
		return x.repo.AnswerCommand(ctx, c.ID, refused(refuseMailboxNotExecuted, "The mailbox belongs to no workspace."))
	}
	m, err := x.svc.repo.GetByAccount(ctx, acc.ID)
	if err != nil {
		return err
	}
	if m == nil || m.InstanceID != c.InstanceID || m.Executor.OrCloud() != models.PoolLinkExecutorInstance || m.EnrollmentState == "pending_remove" {
		return x.repo.AnswerCommand(ctx, c.ID, refused(refuseMailboxNotExecuted, "This instance no longer runs warmup for the mailbox."))
	}
	raw, err := x.sealer.Open(ctx, *acc.OrganizationID, c.Payload)
	if err != nil {
		return err
	}
	var cmd models.PoolLinkCommand
	if err := json.Unmarshal([]byte(raw), &cmd); err != nil || cmd.ID != c.ID {
		return x.repo.AnswerCommand(ctx, c.ID, refused(refuseInvalidCommand, "The command could not be read."))
	}
	if acc.Status != "active" {
		return x.repo.AnswerCommand(ctx, c.ID, refused(refuseMailboxInactive, "The mailbox is not active on this instance."))
	}
	if acc.WorkerID == nil {
		return x.repo.AnswerCommand(ctx, c.ID, refused(refuseWorkerOffline, "No worker on this instance holds the mailbox."))
	}
	if x.liveness != nil {
		live, err := x.liveness.IsWorkerLive(ctx, *acc.WorkerID)
		if err != nil {
			return err
		}
		if !live {
			return x.repo.AnswerCommand(ctx, c.ID, refused(refuseWorkerOffline, "The worker holding the mailbox is offline."))
		}
	}
	switch {
	case c.Kind == models.PoolLinkCommandSend && cmd.Kind == models.PoolLinkCommandSend && cmd.Send != nil:
		return x.executeSend(ctx, c, cmd, acc)
	case c.Kind == models.PoolLinkCommandAction && cmd.Kind == models.PoolLinkCommandAction && cmd.Action != nil:
		return x.executeAction(ctx, c, cmd, acc)
	}
	return x.repo.AnswerCommand(ctx, c.ID, refused(refuseInvalidCommand, "The command kind is not one this instance runs."))
}

// releaseUnstartedSend gives back a send's reservation the worker never began; true when it had begun.
func (x *Execution) releaseUnstartedSend(ctx context.Context, c repository.CloudCommand) (bool, error) {
	r, err := x.repo.SendReservation(ctx, c.ID)
	if err != nil || r == nil {
		return false, err
	}
	if r.Started && !r.Released {
		return true, nil
	}
	if r.Worker != nil && r.Nonce != nil && !r.Released {
		// Refuses nothing the worker has begun: the release and BeginOutbound share one lock.
		if err := x.admission.CancelOutbound(ctx, c.ID, c.EmailAccountID, *r.Worker, *r.Nonce); err != nil {
			return false, err
		}
		if r, err = x.repo.SendReservation(ctx, c.ID); err != nil {
			return false, err
		}
		if r != nil && r.Started && !r.Released {
			return true, nil
		}
	}
	return false, x.repo.CancelSendTask(ctx, c.ID)
}

// validSend returns the one bare recipient of a send whose envelope cannot inject headers.
func validSend(s *models.PoolLinkSendCommand) (string, bool) {
	if s == nil || s.MessageID == "" || s.WarmupToken == "" || s.BodyPlain == "" {
		return "", false
	}
	for _, v := range append([]string{s.To, s.Subject, s.MessageID, s.InReplyTo, s.ThreadID, s.WarmupToken, s.FromName}, s.References...) {
		if strings.ContainsAny(v, "\r\n\x00") {
			return "", false
		}
	}
	addr, err := mail.ParseAddress(s.To)
	if err != nil || addr.Address == "" || strings.Contains(s.To, ",") {
		return "", false
	}
	return addr.Address, true
}

func (x *Execution) executeSend(ctx context.Context, c repository.CloudCommand, cmd models.PoolLinkCommand, acc *models.Email) error {
	s := cmd.Send
	to, ok := validSend(s)
	if !ok {
		return x.repo.AnswerCommand(ctx, c.ID, refused(refuseInvalidCommand, "The send command is malformed."))
	}
	task, err := x.repo.PrepareSendTask(ctx, c.ID, acc.ID, s.MessageID)
	if err != nil {
		return err
	}
	if task.Released || (task.Nonce == nil && task.Status != "active") {
		return x.repo.AnswerCommand(ctx, c.ID, models.PoolLinkCommandResult{Outcome: models.PoolLinkOutcomeFailed, ErrorCode: "send_released", ErrorMessage: "The send was released before it ran."})
	}
	nonce := task.Nonce
	if nonce == nil {
		n, err := x.admission.ReserveOutbound(ctx, repository.OutboundReservation{
			TaskID: c.ID, MailboxID: acc.ID, OrganizationID: *acc.OrganizationID, WorkerID: *acc.WorkerID,
			Provider: models.InboxProvider(acc.Provider), Recipients: []string{to},
		})
		if errors.Is(err, repository.ErrRecipientSuppressed) {
			if cerr := x.repo.CancelSendTask(ctx, c.ID); cerr != nil {
				return cerr
			}
			return x.repo.AnswerCommand(ctx, c.ID, refused(strings.ToLower(repository.SendAdmissionCode(err)), "The recipient is on this workspace's suppression list."))
		}
		if errors.Is(err, repository.ErrSendAdmissionDenied) {
			// Another send's hold or a full window can clear before the deadline; expiry answers it otherwise.
			return x.repo.RetryCommand(ctx, c.ID, x.now().Add(commandRetryAfter), strings.ToLower(repository.SendAdmissionCode(err)))
		}
		if err != nil {
			return err
		}
		nonce = &n
	}
	fromName := strings.TrimSpace(acc.Name)
	if fromName == "" {
		fromName = acc.Email
	}
	params := &events.SendEmailParams{
		TaskID: c.ID, EmailID: acc.ID, OrgID: *acc.OrganizationID,
		To: []string{to}, InReplyTo: s.InReplyTo, References: s.References, ThreadID: s.ThreadID,
		DispatchNonce: nonce.String(), Subject: s.Subject, MessageID: s.MessageID, BodyPlain: s.BodyPlain,
		IsWarmup: true, WarmupToken: s.WarmupToken, FromName: fromName,
	}
	// A re-claimed command republishes the same reservation; the worker runs it at most once.
	if err := x.publisher.PublishSendEmail(ctx, *acc.WorkerID, params); err != nil {
		return fmt.Errorf("publish send: %w", err)
	}
	return x.repo.MarkDispatched(ctx, c.ID, c.NotAfter)
}

// knownActions are the warmup actions a worker runs; anything else is dropped.
var knownActions = []string{
	models.WarmupActionFile, models.WarmupActionRescueFromSpam, models.WarmupActionMarkRead, models.WarmupActionMarkImportant,
	models.WarmupActionStar, models.WarmupActionDelete, models.WarmupActionVerifyRemoval,
}

func (x *Execution) executeAction(ctx context.Context, c repository.CloudCommand, cmd models.PoolLinkCommand, acc *models.Email) error {
	a := cmd.Action
	var actions []string
	for _, act := range a.Actions {
		if slices.Contains(knownActions, act) && !slices.Contains(actions, act) {
			actions = append(actions, act)
		}
	}
	if len(actions) == 0 {
		return x.repo.AnswerCommand(ctx, c.ID, refused(refuseNoActions, "The command named no action this instance runs."))
	}
	placement, folder := acc.WarmupFiling()
	if a.Placement != "" {
		placement, folder = a.Placement, a.TargetFolder
	}
	userID, _ := uuid.Parse(acc.UserID)
	action := models.WarmupEmailAction{UserID: userID, EmailID: acc.ID, RFCMessageID: a.RFCMessageID, Actions: actions,
		Placement: placement, TargetFolder: folder, Recheck: a.Recheck}
	if a.MessageHandle != "" {
		h, err := uuid.Parse(a.MessageHandle)
		if err != nil {
			return x.repo.AnswerCommand(ctx, c.ID, refused(refuseUnknownHandle, "The message handle is not one this instance issued."))
		}
		msg, err := x.repo.WarmupMessageByHandle(ctx, acc.ID, h)
		if err != nil {
			return err
		}
		if msg == nil {
			return x.repo.AnswerCommand(ctx, c.ID, refused(refuseUnknownHandle, "The message handle is not one this instance issued."))
		}
		action.UserID, action.GmailID, action.UID, action.MailboxUIDValidity = msg.UserID, msg.GmailID, msg.UID, msg.UIDValidity
		action.MailboxFolder, action.RFCMessageID, action.InternalID = msg.FolderPath, msg.RFCMessageID, msg.InternalID.String()
	} else {
		// Without a handle the only message Cloud may name is this mailbox's own copy of a send it executed.
		if a.RFCMessageID == "" || strings.ContainsAny(a.RFCMessageID, "\r\n\x00") {
			return x.repo.AnswerCommand(ctx, c.ID, refused(refuseUnknownHandle, "The action names no message this instance reported or sent."))
		}
		own, err := x.repo.IsOwnCloudSend(ctx, acc.ID, a.RFCMessageID)
		if err != nil {
			return err
		}
		if !own {
			return x.repo.AnswerCommand(ctx, c.ID, refused(refuseUnknownHandle, "The action names no message this instance reported or sent."))
		}
	}
	action.CommandID = c.ID.String()
	// The worker's admission window opens before the action can reach it, and only once.
	began, err := x.repo.BeginAction(ctx, c.ID, c.NotAfter)
	if err != nil || !began {
		return err
	}
	if err := x.publisher.PublishWarmupAction(ctx, *acc.WorkerID, &action); err != nil {
		if rerr := x.repo.RevertAction(ctx, c.ID); rerr != nil {
			return errors.Join(fmt.Errorf("publish action: %w", err), rerr)
		}
		return fmt.Errorf("publish action: %w", err)
	}
	// Answered once the worker was admitted to run it (settleAdmitted), or by its own report.
	return nil
}

// settleAdmitted answers done for actions the worker was admitted to run and that report nothing back.
func (x *Execution) settleAdmitted(ctx context.Context) {
	admitted, err := x.repo.AdmittedActions(ctx, commandLeaseLimit)
	if err != nil {
		log.Warn().Err(err).Msg("cloud commands: admitted actions could not be listed")
		return
	}
	for _, c := range admitted {
		if err := x.repo.AnswerCommand(ctx, c.ID, models.PoolLinkCommandResult{Outcome: models.PoolLinkOutcomeDone}); err != nil {
			log.Warn().Err(err).Str("command_id", c.ID.String()).Msg("cloud commands: admitted action not answered")
		}
	}
}

// settleAbandoned answers commands whose local outcome is decided without a worker answer.
func (x *Execution) settleAbandoned(ctx context.Context) {
	stuck, err := x.repo.AbandonedCommands(ctx, commandAnswerGrace, commandLeaseLimit)
	if err != nil {
		log.Warn().Err(err).Msg("cloud commands: abandoned commands could not be listed")
		return
	}
	for _, c := range stuck {
		result, settled, err := x.abandonedResult(ctx, c)
		if err != nil || !settled {
			if err != nil {
				log.Warn().Err(err).Str("command_id", c.ID.String()).Msg("cloud commands: abandoned command not settled")
			}
			continue
		}
		if err := x.repo.AnswerCommand(ctx, c.ID, result); err != nil {
			log.Warn().Err(err).Str("command_id", c.ID.String()).Msg("cloud commands: abandoned command not answered")
		}
	}
}

// abandonedResult decides what an unanswered command came to; settled is false while a begun send may still report.
func (x *Execution) abandonedResult(ctx context.Context, c repository.CloudCommand) (models.PoolLinkCommandResult, bool, error) {
	if c.Kind != models.PoolLinkCommandSend {
		if !c.Admitted {
			return models.PoolLinkCommandResult{Outcome: models.PoolLinkOutcomeFailed, ErrorCode: "not_admitted", ErrorMessage: "This instance's worker did not accept the action."}, true, nil
		}
		return models.PoolLinkCommandResult{Outcome: models.PoolLinkOutcomeFailed, ErrorCode: "no_worker_answer", ErrorMessage: "The worker did not report what became of the action."}, true, nil
	}
	begun, err := x.releaseUnstartedSend(ctx, c)
	if err != nil {
		return models.PoolLinkCommandResult{}, false, err
	}
	if !begun {
		return models.PoolLinkCommandResult{Outcome: models.PoolLinkOutcomeFailed, ErrorCode: "send_released", ErrorMessage: "The send was released before it ran."}, true, nil
	}
	if x.now().Sub(c.NotAfter) < 24*time.Hour {
		return models.PoolLinkCommandResult{}, false, nil
	}
	// A begun send keeps the generic unknown-send path; Cloud is told it has no result.
	return models.PoolLinkCommandResult{Outcome: models.PoolLinkOutcomeFailed, ErrorCode: "send_unresolved", ErrorMessage: "The send ended without a result from this instance's worker."}, true, nil
}

func (x *Execution) deliverReports(ctx context.Context) {
	reports, err := x.repo.ClaimReports(ctx, reportBatch, commandClaimLease)
	if err != nil {
		log.Warn().Err(err).Msg("cloud commands: outbox claim failed")
		return
	}
	links := map[uuid.UUID]*models.CloudLink{}
	for _, r := range reports {
		l, ok := links[r.InstanceID]
		if !ok {
			l, err = x.svc.repo.GetByInstance(ctx, r.InstanceID)
			if err != nil {
				_ = x.repo.ReportFailed(ctx, r.ID, x.now().Add(commandRetryAfter), "link unreadable")
				continue
			}
			links[r.InstanceID] = l
		}
		if l == nil {
			// The link is gone; nothing is left to tell.
			_ = x.repo.ReportDelivered(ctx, r.ID)
			continue
		}
		var path string
		switch {
		case r.Kind == "result" && r.CommandID != nil:
			path = "/instance/commands/" + r.CommandID.String() + "/result"
		case r.Kind == "observation" && r.RemoteID != nil:
			path = "/instance/mailboxes/" + r.RemoteID.String() + "/warmup-observations"
		default:
			_ = x.repo.ReportDelivered(ctx, r.ID)
			continue
		}
		xerr := x.post(ctx, l, path, r.Body, nil)
		if xerr == nil || reportSettled(xerr) {
			if xerr != nil {
				log.Warn().Str("report_id", r.ID.String()).Str("code", xerr.Identifier).Msg("cloud commands: Cloud declined a report; not retried")
			}
			if err := x.repo.ReportDelivered(ctx, r.ID); err != nil {
				log.Warn().Err(err).Str("report_id", r.ID.String()).Msg("cloud commands: delivered report not recorded")
			}
			continue
		}
		_ = x.repo.ReportFailed(ctx, r.ID, x.now().Add(reportBackoff(r.Attempts)), xerr.Identifier)
	}
}

// reportSettled is a refusal no retry can change: the command is answered, gone, or the body refused.
func reportSettled(xerr *errx.Error) bool {
	switch xerr.Code {
	case errx.Conflict, errx.NotFound, errx.BadRequest, errx.Unprocessable:
		return true
	}
	return false
}

func reportBackoff(attempts int) time.Duration {
	d := 30 * time.Second
	for i := 1; i < attempts && d < reportMaxDelay; i++ {
		d *= 2
	}
	return min(d, reportMaxDelay)
}

func (x *Execution) post(ctx context.Context, l *models.CloudLink, path string, body, out any) *errx.Error {
	return x.svc.clientFor(l).do(ctx, http.MethodPost, path, body, out)
}

// SendResolved answers the Cloud command behind a cloud_warmup task with the worker's outcome.
func (x *Execution) SendResolved(ctx context.Context, taskID uuid.UUID, result models.SendEmailResult) error {
	if x == nil {
		return nil
	}
	c, err := x.repo.CommandForTask(ctx, taskID)
	if err != nil || c == nil {
		return err
	}
	return x.repo.AnswerCommand(ctx, c.ID, sendOutcome(result))
}

func sendOutcome(result models.SendEmailResult) models.PoolLinkCommandResult {
	if result.Success {
		return models.PoolLinkCommandResult{Outcome: models.PoolLinkOutcomeSent, MessageID: result.MessageID,
			ProviderMsgID: result.ProviderMsgID, ThreadID: result.ThreadID, SentAt: result.SentAt}
	}
	out := models.PoolLinkCommandResult{Outcome: models.PoolLinkOutcomeFailed, ErrorCode: "send_failed"}
	if e := result.Error; e != nil {
		if e.Code != "" {
			out.ErrorCode = e.Code
		}
		out.ErrorMessage = e.UserTitle
		if e.Failure != nil {
			out.Permanent = e.Failure.Disposition == errx.SendPermanent || e.Failure.Disposition == errx.SendAuth
		}
	}
	if len(out.ErrorMessage) > maxErrorMessage {
		out.ErrorMessage = strings.ToValidUTF8(out.ErrorMessage[:maxErrorMessage], "")
	}
	return out
}

// RescueResolved answers a remove_from_spam command with what the worker found.
func (x *Execution) RescueResolved(ctx context.Context, accountID, internalID uuid.UUID, outcome string) error {
	if x == nil || !models.ValidWarmupRescueOutcome(outcome) {
		return nil
	}
	msg, err := x.repo.ReportedWarmupMessage(ctx, accountID, internalID)
	if err != nil || msg == nil {
		return err
	}
	c, err := x.repo.DispatchedAction(ctx, accountID, models.WarmupActionRescueFromSpam, &msg.Handle, "")
	if err != nil || c == nil {
		return err
	}
	return x.repo.AnswerCommand(ctx, c.ID, models.PoolLinkCommandResult{Outcome: models.PoolLinkOutcomeDone, Rescue: outcome})
}

// RemovalChecked answers a verify_removal command with where the message was found.
func (x *Execution) RemovalChecked(ctx context.Context, accountID uuid.UUID, rfcMessageID, outcome string) error {
	if x == nil || rfcMessageID == "" {
		return nil
	}
	c, err := x.repo.DispatchedAction(ctx, accountID, models.WarmupActionVerifyRemoval, nil, rfcMessageID)
	if err != nil || c == nil {
		return err
	}
	return x.repo.AnswerCommand(ctx, c.ID, models.PoolLinkCommandResult{Outcome: models.PoolLinkOutcomeDone, Removal: outcome})
}
