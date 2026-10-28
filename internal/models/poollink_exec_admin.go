package models

import (
	"time"

	"github.com/google/uuid"
)

// PoolLinkSelfExecutionMailbox is an instance-executed mailbox as the admin panel reads it.
type PoolLinkSelfExecutionMailbox struct {
	EmailAccountID  uuid.UUID  `json:"email_account_id"`
	Email           string     `json:"email"`
	TrustState      string     `json:"trust_state"`
	ReportedSamples int        `json:"reported_samples"`
	Divergence      *float64   `json:"divergence"`
	EvaluatedAt     *time.Time `json:"evaluated_at"`
	LastLeasedAt    *time.Time `json:"last_leased_at"`
}
