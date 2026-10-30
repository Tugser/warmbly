package cloudlink

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// Actions whose provider echo must not read as the owner's doing.
var (
	ownMoveActions = []string{models.WarmupActionFile, models.WarmupActionRescueFromSpam, models.WarmupActionDelete}
	ownFlagActions = []string{models.WarmupActionMarkRead, models.WarmupActionMarkImportant, models.WarmupActionStar}
)

// executedHere is the enrollment and link of a mailbox whose warmup this
// instance runs for a Cloud that speaks the command protocol, else nil.
func (x *Execution) executedHere(ctx context.Context, accountID uuid.UUID) (*models.CloudLinkMailbox, *models.CloudLink, error) {
	if x == nil {
		return nil, nil, nil
	}
	m, err := x.svc.repo.GetByAccount(ctx, accountID)
	if err != nil || m == nil || m.Executor.OrCloud() != models.PoolLinkExecutorInstance || m.EnrollmentState == "pending_remove" {
		return nil, nil, err
	}
	l, err := x.svc.repo.GetByInstance(ctx, m.InstanceID)
	if err != nil || l == nil || l.DisconnectPending {
		return nil, nil, err
	}
	info, xerr := x.svc.selfExecution(ctx, l, false)
	if xerr != nil {
		return nil, nil, xerr
	}
	if !selfExecutionSupported(info) {
		return nil, nil, nil
	}
	return m, l, nil
}

// OwnSentCopy reports a mailbox's own sent copy of a Cloud warmup send, kept out of the unibox.
func (x *Execution) OwnSentCopy(ctx context.Context, msg *models.EmailMessageStoreData) (bool, error) {
	if x == nil || msg == nil || msg.MessageID == "" || !isSentCopy(msg) {
		return false, nil
	}
	return x.repo.IsOwnCloudSend(ctx, msg.EmailID, msg.MessageID)
}

func isSentCopy(m *models.EmailMessageStoreData) bool {
	return models.NormalizeFolder(m.Folder, m.Flags) == models.FolderSent || m.ProviderFolder == models.FolderSent
}

// ReportArrival tells Cloud about warmup mail the consumer already recognised
// in an instance-executed mailbox. handled means Cloud took it and decides
// what happens to it next; otherwise the caller files it as before.
func (x *Execution) ReportArrival(ctx context.Context, userID uuid.UUID, msg *models.EmailMessageStoreData, token, sender string) (bool, error) {
	if x == nil || msg == nil || msg.MessageID == "" || isSentCopy(msg) {
		return false, nil
	}
	m, l, err := x.executedHere(ctx, msg.EmailID)
	if err != nil || m == nil {
		return false, err
	}
	folder := models.NormalizeFolder(msg.Folder, msg.Flags)
	if folder == models.FolderTrash || msg.ProviderFolder == models.FolderTrash {
		return false, nil
	}
	handle, err := x.repo.UpsertWarmupMessage(ctx, repository.CloudWarmupMessage{
		Handle: uuid.New(), EmailAccountID: msg.EmailID, UserID: userID, InternalID: msg.ID, RFCMessageID: msg.MessageID,
		GmailID: msg.GmailID, UID: msg.UID, UIDValidity: msg.Mailbox, FolderPath: msg.FolderPath, Folder: folder,
	})
	if err != nil {
		return false, fmt.Errorf("record warmup handle: %w", err)
	}
	received := msg.InternalDate
	if received.IsZero() {
		received = x.now()
	}
	arrival := models.PoolLinkWarmupArrival{
		MessageHandle: handle.String(), MessageID: msg.MessageID, WarmupToken: token, From: sender, Subject: msg.Subject,
		InReplyTo: parentIDs(msg.InReplyTo), Folder: folder,
		LandedSpam: models.ClassifyWarmupLanding(folder, msg.Flags) == models.WarmupLandedSpam,
		Flags:      systemFlags(msg.Flags), ReceivedAt: received.UTC(),
	}
	var verdict models.PoolLinkArrivalVerdict
	if xerr := x.post(ctx, l, "/instance/mailboxes/"+m.RemoteID.String()+"/warmup-arrivals", arrival, &verdict); xerr != nil {
		return false, xerr
	}
	switch verdict.Verdict {
	case models.PoolLinkArrivalAccepted, models.PoolLinkArrivalDuplicate:
		if err := x.repo.MarkWarmupMessageReported(ctx, handle); err != nil {
			return false, fmt.Errorf("mark warmup handle reported: %w", err)
		}
		return true, nil
	default:
		if err := x.repo.ForgetWarmupMessage(ctx, handle); err != nil {
			return false, fmt.Errorf("forget warmup handle: %w", err)
		}
		return false, nil
	}
}

// ObserveRemoval tells Cloud a reported warmup message left its folder, unless this instance moved it.
func (x *Execution) ObserveRemoval(ctx context.Context, accountID, internalID uuid.UUID) error {
	return x.observe(ctx, accountID, internalID, models.PoolLinkObservationRemoved, "", ownMoveActions)
}

// ObserveFlags tells Cloud about a spam flag or a read or star change on a reported warmup message.
func (x *Execution) ObserveFlags(ctx context.Context, accountID, internalID uuid.UUID, flags []string, added bool) error {
	switch {
	case added && models.HasSpamFlag(flags):
		return x.observe(ctx, accountID, internalID, models.PoolLinkObservationMovedToSpam, models.FolderSpam, ownMoveActions)
	case slices.Contains(flags, models.FlagSeen) || slices.Contains(flags, models.FlagFlagged):
		// Only adding a flag can echo an action of ours; this instance never unreads or unstars.
		echo := ownFlagActions
		if !added {
			echo = nil
		}
		return x.observe(ctx, accountID, internalID, models.PoolLinkObservationOwnerActive, "", echo)
	}
	return nil
}

// ObserveFolder tells Cloud a reported warmup message moved into spam.
func (x *Execution) ObserveFolder(ctx context.Context, accountID, internalID uuid.UUID, folder string) error {
	if folder != models.FolderSpam {
		return nil
	}
	return x.observe(ctx, accountID, internalID, models.PoolLinkObservationMovedToSpam, folder, ownMoveActions)
}

func (x *Execution) observe(ctx context.Context, accountID, internalID uuid.UUID, kind, folder string, ownEcho []string) error {
	if x == nil {
		return nil
	}
	msg, err := x.repo.ReportedWarmupMessage(ctx, accountID, internalID)
	if err != nil || msg == nil {
		return err
	}
	m, l, err := x.executedHere(ctx, accountID)
	if err != nil || m == nil {
		return err
	}
	if len(ownEcho) > 0 {
		own, err := x.repo.RecentOwnAction(ctx, accountID, msg.Handle, ownEcho, ownMoveWindow)
		if err != nil || own {
			return err
		}
	}
	return x.repo.QueueObservation(ctx, l.InstanceID, accountID, m.RemoteID, models.PoolLinkWarmupObservation{
		MessageHandle: msg.Handle.String(), MessageID: msg.RFCMessageID, Kind: kind, Folder: folder, ObservedAt: x.now().UTC(),
	})
}

// systemFlags keeps the protocol's flags and drops the owner's own keywords.
func systemFlags(flags []string) []string {
	var out []string
	for _, f := range flags {
		if strings.HasPrefix(f, `\`) || strings.HasPrefix(f, "$") {
			out = append(out, f)
		}
	}
	return out
}

func parentIDs(inReplyTo []string) string {
	for _, raw := range inReplyTo {
		for _, id := range strings.Fields(raw) {
			if id = strings.Trim(id, "<>"); id != "" {
				return id
			}
		}
	}
	return ""
}
