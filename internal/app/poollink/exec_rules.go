package poollink

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/warmbly/warmbly/internal/app/opsnotify"
	"github.com/warmbly/warmbly/internal/config"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

const opsEventReporterUntrusted = opsnotify.EventWarmupReporterUntrusted

// Reasons a warmup arrival report is rejected.
const (
	ReasonTokenUnknown       = "token_unknown"
	ReasonTokenOtherMailbox  = "token_other_mailbox"
	ReasonSentCopy           = "sent_copy"
	ReasonTokenExpired       = "token_expired"
	ReasonSenderMismatch     = "sender_mismatch"
	ReasonMessageIDMismatch  = "message_id_mismatch"
	ReasonReceivedBeforeSend = "received_before_send"
	ReasonReceivedInFuture   = "received_in_future"
	ReasonSelfExecutedPair   = "self_executed_pair"
)

// arrivalEvidence is the send an arrival claims to be, as Cloud recorded it.
type arrivalEvidence struct {
	Token          *models.WarmupToken
	TokenPresented bool
	SenderEmail    string
	// SentIDs are the Message-IDs the send may carry: the one its result recorded and, once vouched for, the minted one.
	SentIDs []string
	// Settled is false until the send's result is applied, when a re-stamped Message-ID is not yet known.
	Settled bool
	// SenderExecuted holds when a linked instance executes the sender, so both ends would be self-attested.
	SenderExecuted bool
}

// arrivalEvidenceFor assembles the evidence; the minted Message-ID counts only once a Cloud worker took the send or a sent result is recorded.
func arrivalEvidenceFor(token *models.WarmupToken, presented bool, sender *models.Email, send repository.WarmupTaskSend) arrivalEvidence {
	ev := arrivalEvidence{Token: token, TokenPresented: presented, Settled: send.Settled, SenderExecuted: send.SenderExecuted}
	if sender != nil {
		ev.SenderEmail = sender.Email
	}
	if token.SentMessageID != "" {
		ev.SentIDs = append(ev.SentIDs, token.SentMessageID)
	}
	if send.MintedID != "" && (send.CloudExecuted || send.SentRecorded) {
		ev.SentIDs = append(ev.SentIDs, send.MintedID)
	}
	return ev
}

// checkArrival accepts a report only as the recipient-bound delivery of a send Cloud made; pending asks for a retry.
func checkArrival(account uuid.UUID, a models.PoolLinkWarmupArrival, p arrivalEvidence, now time.Time) (verdict *models.PoolLinkArrivalVerdict, pending bool) {
	reject := func(reason string) (*models.PoolLinkArrivalVerdict, bool) {
		return &models.PoolLinkArrivalVerdict{Verdict: models.PoolLinkArrivalRejected, Reason: reason}, false
	}
	t := p.Token
	if t == nil {
		return reject(ReasonTokenUnknown)
	}
	if t.RecipientAccountID != account {
		if t.SenderAccountID == account {
			return reject(ReasonSentCopy)
		}
		return reject(ReasonTokenOtherMailbox)
	}
	if p.SenderExecuted {
		return reject(ReasonSelfExecutedPair)
	}
	if p.SenderEmail == "" || !strings.EqualFold(senderAddress(a.From), strings.TrimSpace(p.SenderEmail)) {
		return reject(ReasonSenderMismatch)
	}
	skew := config.PoolLinkArrivalClockSkewMinutes * time.Minute
	if a.ReceivedAt.Before(t.CreatedAt.Add(-skew)) {
		return reject(ReasonReceivedBeforeSend)
	}
	if a.ReceivedAt.After(now.Add(skew)) {
		return reject(ReasonReceivedInFuture)
	}
	if !t.ExpiresAt.IsZero() && a.ReceivedAt.After(t.ExpiresAt) {
		return reject(ReasonTokenExpired)
	}
	if !slices.ContainsFunc(p.SentIDs, func(id string) bool { return sameMessageID(id, a.MessageID) }) {
		if !p.Settled {
			return nil, true
		}
		return reject(ReasonMessageIDMismatch)
	}
	return &models.PoolLinkArrivalVerdict{Verdict: models.PoolLinkArrivalAccepted}, false
}

func bareMessageID(id string) string { return strings.Trim(strings.TrimSpace(id), "<>") }

func sameMessageID(a, b string) bool {
	a, b = bareMessageID(a), bareMessageID(b)
	return a != "" && strings.EqualFold(a, b)
}

// validMessageID holds for a single-line Message-ID of header length.
func validMessageID(id string) bool {
	bare := bareMessageID(id)
	return bare != "" && len(id) <= 998 && strings.Contains(bare, "@") && !strings.ContainsAny(id, "\r\n\x00 ")
}

func bracketMessageID(id string) string {
	if bare := bareMessageID(id); bare != "" {
		return "<" + bare + ">"
	}
	return ""
}

// senderAddress is the bare address of a From header value.
func senderAddress(from string) string {
	if addr, err := mail.ParseAddress(from); err == nil {
		return strings.TrimSpace(addr.Address)
	}
	return strings.Trim(strings.TrimSpace(from), "<>")
}

// arrivalInternalID is stable per reported message, so a replayed report maps to the same stored mail.
func arrivalInternalID(account uuid.UUID, handle string) uuid.UUID {
	return uuid.NewSHA1(account, []byte("pool-link-arrival:"+handle))
}

// arrivalEvent is the NEW_EMAIL a worker would have produced for the reported mail; only Cloud's own verified token rides in its flags.
func arrivalEvent(m *repository.PoolLinkExecMailbox, a models.PoolLinkWarmupArrival, internal uuid.UUID, tokenPresented bool, token uuid.UUID, now time.Time) *models.JobEvent {
	folder := models.FolderInbox
	switch {
	case a.LandedSpam || strings.EqualFold(strings.TrimSpace(a.Folder), models.FolderSpam):
		folder = models.FolderSpam
	case strings.EqualFold(strings.TrimSpace(a.Folder), models.FolderArchive):
		folder = models.FolderArchive
	}
	flags := []string{}
	for _, f := range a.Flags {
		f = strings.ToUpper(strings.TrimSpace(f))
		if strings.HasPrefix(f, "CATEGORY_") && len(f) <= 64 && !strings.ContainsAny(f, ":\r\n") && !slices.Contains(flags, f) {
			flags = append(flags, f)
		}
	}
	if tokenPresented {
		flags = append(flags, config.WarmupVerifyHeader+": "+token.String())
	}
	inReplyTo := []string{}
	if parent := bracketMessageID(a.InReplyTo); parent != "" {
		inReplyTo = append(inReplyTo, parent)
	}
	msg := &models.EmailMessageStoreData{
		ID: internal, EmailID: m.EmailAccountID, Folder: folder, ProviderFolder: folder,
		MessageID: bracketMessageID(a.MessageID), Flags: flags, BCC: []string{}, CC: []string{}, ReplyTo: []string{},
		FromAddr: []string{senderAddress(a.From)}, ToAddr: []string{m.Email}, InReplyTo: inReplyTo,
		Subject: a.Subject, InternalDate: a.ReceivedAt, SentDate: a.ReceivedAt, CreatedAt: now, UpdatedAt: now,
	}
	return &models.JobEvent{Type: models.JobEventTypeNewEmail, Body: &models.JobEventNewEmail{UserID: m.UserID, Message: msg}}
}

// commandAnswer is what an accepted result stores and the worker events it stands in for.
type commandAnswer struct {
	repository.PoolLinkCommandAnswer
	events []*models.JobEvent
}

// decideAnswer validates an instance's result against the stored command; nil with no error is an identical replay.
func decideAnswer(c *repository.StoredPoolLinkCommand, env *commandEnvelope, r models.PoolLinkCommandResult, now time.Time) (*commandAnswer, *errx.Error) {
	r = normalizeResult(r)
	if !models.ValidPoolLinkOutcome(c.Kind, r.Outcome) {
		return nil, ErrCommandResultInvalid
	}
	switch c.Status {
	case "answered":
		if sameAnswer(c.Result, r) {
			return nil, nil
		}
		return nil, ErrCommandAnswered
	case "expired":
		if r.Outcome == models.PoolLinkOutcomeExpired {
			return nil, nil
		}
		// A send that went out after its deadline is still reported, so the mail is counted.
		if c.Kind != models.PoolLinkCommandSend {
			return nil, ErrCommandAnswered
		}
	case "leased":
	case "pending":
		return nil, ErrCommandNotLeased
	default:
		return nil, ErrCommandAnswered
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return nil, errx.InternalError()
	}
	answer := &commandAnswer{PoolLinkCommandAnswer: repository.PoolLinkCommandAnswer{Status: "answered", Outcome: r.Outcome, Result: raw}}
	switch c.Kind {
	case models.PoolLinkCommandSend:
		if c.TaskID == nil {
			return nil, ErrCommandResultInvalid
		}
		res, xerr := sendResult(*c.TaskID, r, now)
		if xerr != nil {
			return nil, xerr
		}
		answer.SendResult = res
	default:
		if env == nil || env.Origin == nil {
			return nil, ErrCommandResultInvalid
		}
		answer.events = actionEvents(env.Origin, r)
	}
	return answer, nil
}

// sendResult is the worker result a send's answer stands for; nothing in it can place a hold Cloud cannot lift.
func sendResult(task uuid.UUID, r models.PoolLinkCommandResult, now time.Time) (*models.SendEmailResult, *errx.Error) {
	res := &models.SendEmailResult{TaskID: task}
	if r.Outcome == models.PoolLinkOutcomeSent {
		if !validMessageID(r.MessageID) {
			return nil, ErrCommandResultInvalid
		}
		res.Success, res.MessageID, res.ProviderMsgID, res.ThreadID = true, r.MessageID, r.ProviderMsgID, r.ThreadID
		res.SentAt = r.SentAt
		if res.SentAt.IsZero() || res.SentAt.After(now) {
			res.SentAt = now
		}
		return res, nil
	}
	code := r.ErrorCode
	if code == "" {
		code = r.Outcome
	}
	message := "linked instance: " + code
	if r.ErrorMessage != "" {
		message += ": " + r.ErrorMessage
	}
	failure := &errx.SendFailure{Protocol: "pool_link", Stage: "send", Scope: "mailbox", Disposition: errx.SendRetry, ObservedAt: now}
	switch {
	case r.Outcome != models.PoolLinkOutcomeFailed:
		// Refused and expired sends never reached the provider: walked back with no cooldown.
		failure.Protocol, failure.Stage = "internal", "prepare"
	case r.Permanent:
		retry := now.Add(time.Hour)
		failure.RetryAt = &retry
	default:
		retry := now.Add(5 * time.Minute)
		failure.RetryAt = &retry
	}
	res.SentAt = now
	res.Error = &models.EmailSendError{Code: "SEND_FAILED", Type: "pool_link", Message: message, Failure: failure}
	return res, nil
}

// actionEvents are the worker events an action's answer stands for; anything but done reads as not done.
func actionEvents(o *models.WarmupEmailAction, r models.PoolLinkCommandResult) []*models.JobEvent {
	done := r.Outcome == models.PoolLinkOutcomeDone
	var out []*models.JobEvent
	if slices.Contains(o.Actions, models.WarmupActionRescueFromSpam) {
		if id, err := uuid.Parse(o.InternalID); err == nil {
			outcome := models.WarmupRescueFailed
			if done {
				switch r.Rescue {
				case models.WarmupRescueMoved, models.WarmupRescueAlreadyOut, models.WarmupRescueGone, models.WarmupRescueFailed:
					outcome = r.Rescue
				}
			}
			out = append(out, &models.JobEvent{Type: models.JobEventTypeWarmupRescued,
				Body: &models.JobEventWarmupRescued{UserID: o.UserID, EmailID: o.EmailID, InternalID: id, Outcome: outcome}})
		}
	}
	if slices.Contains(o.Actions, models.WarmupActionVerifyRemoval) {
		outcome := models.WarmupRemovalUnknown
		if done {
			switch r.Removal {
			case models.WarmupRemovalPresent, models.WarmupRemovalTrashed, models.WarmupRemovalGone, models.WarmupRemovalUnknown:
				outcome = r.Removal
			}
		}
		out = append(out, &models.JobEvent{Type: models.JobEventTypeWarmupRemovalChecked,
			Body: &models.JobEventWarmupRemovalChecked{UserID: o.UserID, EmailID: o.EmailID, RFCMessageID: o.RFCMessageID, Outcome: outcome, Recheck: o.Recheck}})
	}
	if done && o.FilingID != "" {
		if id, err := uuid.Parse(o.FilingID); err == nil {
			out = append(out, &models.JobEvent{Type: models.JobEventTypeWarmupFiled, Body: &models.JobEventWarmupFiled{EmailID: o.EmailID, FilingID: id}})
		}
	}
	return out
}

// normalizeResult trims a result to what Cloud stores and compares.
func normalizeResult(r models.PoolLinkCommandResult) models.PoolLinkCommandResult {
	r.Outcome = strings.ToLower(strings.TrimSpace(r.Outcome))
	r.MessageID = strings.TrimSpace(r.MessageID)
	r.ProviderMsgID = clip(strings.TrimSpace(r.ProviderMsgID), 512)
	r.ThreadID = clip(strings.TrimSpace(r.ThreadID), 512)
	r.Rescue = strings.ToLower(strings.TrimSpace(r.Rescue))
	r.Removal = strings.ToLower(strings.TrimSpace(r.Removal))
	r.ErrorCode = clip(strings.Map(func(c rune) rune {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '-' {
			return c
		}
		return -1
	}, strings.ToLower(strings.TrimSpace(r.ErrorCode))), 64)
	r.ErrorMessage = clip(strings.TrimSpace(strings.Map(func(c rune) rune {
		if unicode.IsControl(c) || unicode.Is(unicode.Cf, c) {
			return ' '
		}
		return c
	}, r.ErrorMessage)), 300)
	return r
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// sameAnswer reports whether a replayed result says what the stored one does.
func sameAnswer(stored []byte, r models.PoolLinkCommandResult) bool {
	if len(bytes.TrimSpace(stored)) == 0 {
		return false
	}
	var prev models.PoolLinkCommandResult
	if err := json.Unmarshal(stored, &prev); err != nil {
		return false
	}
	return prev.Outcome == r.Outcome && prev.MessageID == r.MessageID && prev.Rescue == r.Rescue &&
		prev.Removal == r.Removal && prev.ErrorCode == r.ErrorCode && prev.Permanent == r.Permanent
}

// nextReporterTrust decides a reporter's standing from its sample, with hysteresis so a reporter near a line does not flap.
func nextReporterTrust(current string, s repository.ReporterTrustSample) string {
	if s.Reported < config.PoolLinkTrustMinReported || s.Observed < config.PoolLinkTrustMinObserved {
		if current == "" {
			return models.PoolLinkReportProbation
		}
		return current
	}
	_, _, divergence := s.Rates()
	switch {
	case divergence >= config.PoolLinkTrustUntrustedDivergence:
		return models.PoolLinkReportUntrusted
	case current == models.PoolLinkReportUntrusted && divergence >= config.PoolLinkTrustProbationDivergence:
		return models.PoolLinkReportUntrusted
	case divergence >= config.PoolLinkTrustProbationDivergence:
		return models.PoolLinkReportProbation
	}
	return models.PoolLinkReportTrusted
}

func percent(f float64) string { return fmt.Sprintf("%.1f%%", f*100) }

// transientValidation tells a check that could not run from a refusal, so the send waits for its next lease instead of being withdrawn.
func transientValidation(err error) bool {
	var xerr *errx.Error
	if errors.As(err, &xerr) {
		return xerr.Code == errx.Internal
	}
	var pgErr *pgconn.PgError
	var netErr net.Error
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.As(err, &pgErr) || errors.As(err, &netErr) || pgconn.Timeout(err) || pgconn.SafeToRetry(err)
}
