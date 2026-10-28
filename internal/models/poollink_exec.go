package models

import (
	"time"

	"github.com/google/uuid"
)

// PoolLinkExecutor says which side runs a linked mailbox's warmup: Cloud with a
// copy of its credential, or the instance that keeps the only credential.
type PoolLinkExecutor string

const (
	PoolLinkExecutorCloud    PoolLinkExecutor = "cloud"
	PoolLinkExecutorInstance PoolLinkExecutor = "instance"
)

// Valid reports an executor this build knows; empty reads as cloud.
func (e PoolLinkExecutor) Valid() bool {
	return e == "" || e == PoolLinkExecutorCloud || e == PoolLinkExecutorInstance
}

// OrCloud resolves the empty value older clients and rows carry.
func (e PoolLinkExecutor) OrCloud() PoolLinkExecutor {
	if e == "" {
		return PoolLinkExecutorCloud
	}
	return e
}

// PoolLinkSelfExecutionProtocol is advertised in PoolLinkInstanceInfo by a Cloud
// that accepts instance-executed enrollment, the command queue and arrival reports.
const PoolLinkSelfExecutionProtocol = 1

// PoolLinkSelfExecution is the instance-facing view of the admin grant.
type PoolLinkSelfExecution struct {
	Protocol  int        `json:"protocol"`
	Granted   bool       `json:"granted"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// Command kinds on the per-link queue.
const (
	PoolLinkCommandSend   = "send"
	PoolLinkCommandAction = "action"
)

// Outcomes an instance reports for a command.
const (
	PoolLinkOutcomeSent    = "sent"
	PoolLinkOutcomeFailed  = "failed"
	PoolLinkOutcomeDone    = "done"
	PoolLinkOutcomeExpired = "expired"
	PoolLinkOutcomeRefused = "refused"
)

// ValidPoolLinkOutcome reports whether an outcome fits the command kind.
func ValidPoolLinkOutcome(kind, outcome string) bool {
	switch outcome {
	case PoolLinkOutcomeExpired, PoolLinkOutcomeRefused, PoolLinkOutcomeFailed:
		return kind == PoolLinkCommandSend || kind == PoolLinkCommandAction
	case PoolLinkOutcomeSent:
		return kind == PoolLinkCommandSend
	case PoolLinkOutcomeDone:
		return kind == PoolLinkCommandAction
	}
	return false
}

// PoolLinkCommand is one unit of work Cloud hands an instance for a mailbox it executes.
type PoolLinkCommand struct {
	ID       uuid.UUID              `json:"id"`
	RemoteID uuid.UUID              `json:"remote_id"`
	Kind     string                 `json:"kind"`
	Send     *PoolLinkSendCommand   `json:"send,omitempty"`
	Action   *PoolLinkActionCommand `json:"action,omitempty"`
	// NotAfter is when a command still unexecuted must be answered expired instead.
	NotAfter  time.Time `json:"not_after"`
	CreatedAt time.Time `json:"created_at"`
}

// PoolLinkSendCommand is a warmup message, rendered by Cloud, the instance sends verbatim.
type PoolLinkSendCommand struct {
	To          string   `json:"to"`
	Subject     string   `json:"subject"`
	BodyPlain   string   `json:"body_plain"`
	BodyHTML    string   `json:"body_html,omitempty"`
	MessageID   string   `json:"message_id"`
	InReplyTo   string   `json:"in_reply_to,omitempty"`
	References  []string `json:"references,omitempty"`
	ThreadID    string   `json:"thread_id,omitempty"`
	WarmupToken string   `json:"warmup_token"`
	FromName    string   `json:"from_name,omitempty"`
}

// PoolLinkActionCommand is a warmup action on a message the instance reported.
type PoolLinkActionCommand struct {
	// MessageHandle is the opaque handle from the arrival report, or empty for
	// the mailbox's own sent copy, which the instance resolves by RFCMessageID.
	MessageHandle string   `json:"message_handle,omitempty"`
	RFCMessageID  string   `json:"rfc_message_id"`
	Actions       []string `json:"actions"`
	Placement     string   `json:"placement,omitempty"`
	TargetFolder  string   `json:"target_folder,omitempty"`
	Recheck       bool     `json:"recheck,omitempty"`
}

// PoolLinkLeaseRequest asks for up to Limit due commands.
type PoolLinkLeaseRequest struct {
	Limit int `json:"limit"`
}

// PoolLinkLeaseResponse carries the leased commands and when the lease lapses.
type PoolLinkLeaseResponse struct {
	Commands    []PoolLinkCommand `json:"commands"`
	LeasedUntil time.Time         `json:"leased_until"`
}

// PoolLinkCommandResult is the instance's answer to one command.
type PoolLinkCommandResult struct {
	Outcome       string    `json:"outcome"`
	MessageID     string    `json:"message_id,omitempty"`
	ProviderMsgID string    `json:"provider_msg_id,omitempty"`
	ThreadID      string    `json:"thread_id,omitempty"`
	SentAt        time.Time `json:"sent_at,omitempty"`
	ErrorCode     string    `json:"error_code,omitempty"`
	ErrorMessage  string    `json:"error_message,omitempty"`
	// Permanent marks a failure retrying the same mailbox cannot fix (auth, policy).
	Permanent bool `json:"permanent,omitempty"`
	// Rescue is a WarmupRescue* outcome for a remove_from_spam action.
	Rescue string `json:"rescue,omitempty"`
	// Removal is a WarmupRemoval* answer for a verify_removal action.
	Removal string `json:"removal,omitempty"`
}

// PoolLinkWarmupArrival is warmup mail the instance found in a mailbox it executes.
// Only warmup mail is reported; nothing else in the mailbox leaves the instance.
type PoolLinkWarmupArrival struct {
	MessageHandle string    `json:"message_handle"`
	MessageID     string    `json:"message_id"`
	WarmupToken   string    `json:"warmup_token,omitempty"`
	From          string    `json:"from"`
	Subject       string    `json:"subject"`
	InReplyTo     string    `json:"in_reply_to,omitempty"`
	References    []string  `json:"references,omitempty"`
	Folder        string    `json:"folder"`
	LandedSpam    bool      `json:"landed_spam"`
	Flags         []string  `json:"flags,omitempty"`
	ReceivedAt    time.Time `json:"received_at"`
}

// Arrival verdicts Cloud answers with.
const (
	PoolLinkArrivalAccepted  = "accepted"
	PoolLinkArrivalDuplicate = "duplicate"
	PoolLinkArrivalNotWarmup = "not_warmup"
	PoolLinkArrivalRejected  = "rejected"
)

// PoolLinkArrivalVerdict tells the instance whether the report was warmup mail Cloud recognised.
type PoolLinkArrivalVerdict struct {
	Verdict string `json:"verdict"`
	Reason  string `json:"reason,omitempty"`
}

// Kinds of later change an instance reports on warmup mail it already reported.
const (
	PoolLinkObservationRemoved     = "removed"
	PoolLinkObservationMovedToSpam = "moved_to_spam"
	PoolLinkObservationOwnerActive = "owner_activity"
)

// PoolLinkWarmupObservation is a later change to a reported warmup message.
type PoolLinkWarmupObservation struct {
	MessageHandle string    `json:"message_handle"`
	MessageID     string    `json:"message_id"`
	Kind          string    `json:"kind"`
	Folder        string    `json:"folder,omitempty"`
	ObservedAt    time.Time `json:"observed_at"`
}

// Reporter trust states for a self-executed mailbox's placement reports.
const (
	PoolLinkReportTrusted   = "trusted"
	PoolLinkReportProbation = "probation"
	PoolLinkReportUntrusted = "untrusted"
)

// PartnershipPlanID is the private $90 Partnership plan; sold only to a workspace holding an offer for it.
var PartnershipPlanID = uuid.MustParse("00000000-0000-0000-0000-000000000003")

// PlanOffer lets one workspace buy a private plan; an operator grants it.
type PlanOffer struct {
	OrganizationID uuid.UUID  `json:"organization_id"`
	PlanID         uuid.UUID  `json:"plan_id"`
	GrantedBy      *uuid.UUID `json:"granted_by,omitempty"`
	Reason         string     `json:"reason"`
	GrantedAt      time.Time  `json:"granted_at"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	Plan           *Plan      `json:"plan,omitempty"`
}

// Active reports an offer still open at now.
func (o *PlanOffer) Active(now time.Time) bool {
	return o != nil && (o.ExpiresAt == nil || now.Before(*o.ExpiresAt))
}

// SelfExecutionGrant is an operator's permission for a workspace's linked
// instances to enroll mailboxes without handing Cloud their credentials.
type SelfExecutionGrant struct {
	OrganizationID uuid.UUID  `json:"organization_id"`
	GrantedBy      *uuid.UUID `json:"granted_by,omitempty"`
	Reason         string     `json:"reason"`
	GrantedAt      time.Time  `json:"granted_at"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
}

// Active reports a grant still in force at now.
func (g *SelfExecutionGrant) Active(now time.Time) bool {
	return g != nil && (g.ExpiresAt == nil || now.Before(*g.ExpiresAt))
}
