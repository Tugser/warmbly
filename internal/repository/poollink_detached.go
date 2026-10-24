package repository

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/infrastructure/db"
)

// PoolLinkDetachedWarmupKey is the admin_settings row that records the detached-link warmup stop.
const PoolLinkDetachedWarmupKey = "pool_link.detached_warmup_stop"

// DetachedWarmupStop is that row: when the first pass ran, and when a complete pass finished it.
type DetachedWarmupStop struct {
	FencedAt time.Time  `json:"fenced_at"`
	DoneAt   *time.Time `json:"done_at,omitempty"`
}

// DetachedWarmupMailbox is a Cloud mailbox a revoked pool link enrolled that still takes part in warmup.
type DetachedWarmupMailbox struct {
	EmailAccountID uuid.UUID
	OrganizationID uuid.UUID
}

// PoolLinkDetachedRepository finds the mailboxes of revoked pool links that still warm.
type PoolLinkDetachedRepository interface {
	// DetachedWarmupStopState returns the row, fencing it at now on first use.
	DetachedWarmupStopState(ctx context.Context, now time.Time) (*DetachedWarmupStop, error)
	MarkDetachedWarmupStopDone(ctx context.Context, at time.Time) error
	ListDetachedWarmupMailboxes(ctx context.Context) ([]DetachedWarmupMailbox, error)
}

var _ PoolLinkDetachedRepository = (*poolLinkRepository)(nil)

func (r *poolLinkRepository) DetachedWarmupStopState(ctx context.Context, now time.Time) (*DetachedWarmupStop, error) {
	fence, err := json.Marshal(DetachedWarmupStop{FencedAt: now.UTC()})
	if err != nil {
		return nil, err
	}
	if _, err := r.db.Exec(ctx, `INSERT INTO admin_settings (key, value, updated_at) VALUES ($1, $2, now()) ON CONFLICT (key) DO NOTHING`, PoolLinkDetachedWarmupKey, fence); err != nil {
		return nil, err
	}
	var raw []byte
	if err := r.db.QueryRow(ctx, `SELECT value FROM admin_settings WHERE key = $1`, PoolLinkDetachedWarmupKey).Scan(&raw); err != nil {
		return nil, err
	}
	var st DetachedWarmupStop
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

func (r *poolLinkRepository) MarkDetachedWarmupStopDone(ctx context.Context, at time.Time) error {
	done, err := json.Marshal(at.UTC())
	if err != nil {
		return err
	}
	_, err = r.db.Exec(ctx, `UPDATE admin_settings SET value = value || jsonb_build_object('done_at', $2::jsonb), updated_at = now() WHERE key = $1`, PoolLinkDetachedWarmupKey, done)
	return err
}

// ListDetachedWarmupMailboxes reads the managed-operation record each revoked link keeps, skipping mailboxes a live link holds now.
func (r *poolLinkRepository) ListDetachedWarmupMailboxes(ctx context.Context) ([]DetachedWarmupMailbox, error) {
	query := `
		SELECT DISTINCT ea.id, ea.organization_id
		  FROM pool_link_managed_operations op
		  JOIN pool_link_instances i ON i.id = op.instance_id AND i.revoked_at IS NOT NULL
		  JOIN email_accounts ea ON (ea.id = op.email_account_id OR ea.id = op.planned_account_id)
		       AND ea.organization_id = i.organization_id
		 WHERE (ea.test_mode IS NULL OR ea.test_mode = 'legacy'
		        OR ea.test_mode = 'diagnostic' AND (ea.test_send_enabled OR ea.test_receive_enabled))
		   AND NOT EXISTS (SELECT 1 FROM pool_link_mailboxes m JOIN pool_link_instances live ON live.id = m.instance_id
		                    WHERE m.email_account_id = ea.id AND live.revoked_at IS NULL)
		 ORDER BY ea.id`
	rows, err := r.db.Query(ctx, query)
	if err != nil {
		db.CaptureError(err, query, nil, "query")
		return nil, err
	}
	defer rows.Close()
	var out []DetachedWarmupMailbox
	for rows.Next() {
		var m DetachedWarmupMailbox
		if err := rows.Scan(&m.EmailAccountID, &m.OrganizationID); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
