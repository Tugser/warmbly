package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TrackedLink is one minted click-tracking ticket: the email carries only the
// opaque ID, this row holds where it actually goes.
type TrackedLink struct {
	ID          uuid.UUID
	TaskID      uuid.UUID
	CampaignID  uuid.UUID
	Destination string
	// Label is the anchor text the link was minted from ("Pricing"), so a
	// click can be named without re-parsing the email. Empty for image links.
	Label     string
	CreatedAt time.Time
	// OrganizationID is read through the task's mailbox, else the campaign; nil when neither remains.
	OrganizationID *uuid.UUID
}

// TrackedLinkRepository is the server-side click-link store. Only the send
// pipeline writes; the tracking service reads through the backend internal
// API. There is deliberately no update path from any request-facing surface.
type TrackedLinkRepository interface {
	CreateBatch(ctx context.Context, links []TrackedLink) error
	GetByID(ctx context.Context, id uuid.UUID) (*TrackedLink, error)
	Cleanup(ctx context.Context, olderThanDays int) (int64, error)
}

type trackedLinkRepository struct {
	db *pgxpool.Pool
}

// NewTrackedLinkRepository creates a new tracked link repository
func NewTrackedLinkRepository(db *pgxpool.Pool) TrackedLinkRepository {
	return &trackedLinkRepository{db: db}
}

// CreateBatch inserts all minted links for one outgoing email in a single
// round trip. All-or-nothing: the caller falls back to unwrapped links when
// this fails, so a partially-stored email can never ship dead links.
func (r *trackedLinkRepository) CreateBatch(ctx context.Context, links []TrackedLink) error {
	if len(links) == 0 {
		return nil
	}

	rows := make([][]any, 0, len(links))
	for _, l := range links {
		rows = append(rows, []any{l.ID, l.TaskID, l.CampaignID, l.Destination, l.Label})
	}

	_, err := r.db.CopyFrom(ctx,
		pgx.Identifier{"tracked_links"},
		[]string{"id", "task_id", "campaign_id", "destination", "label"},
		pgx.CopyFromRows(rows),
	)
	return err
}

// GetByID resolves a ticket to its destination and owning organization. nil, nil when unknown.
func (r *trackedLinkRepository) GetByID(ctx context.Context, id uuid.UUID) (*TrackedLink, error) {
	query := `
		SELECT l.id, l.task_id, l.campaign_id, l.destination, l.label, l.created_at,
		       COALESCE(ea.organization_id, c.organization_id)
		FROM tracked_links l
		LEFT JOIN tasks t ON t.id = l.task_id
		LEFT JOIN email_accounts ea ON ea.id = t.email_account_id
		LEFT JOIN campaigns c ON c.id = l.campaign_id
		WHERE l.id = $1
	`

	var l TrackedLink
	err := r.db.QueryRow(ctx, query, id).Scan(&l.ID, &l.TaskID, &l.CampaignID, &l.Destination, &l.Label, &l.CreatedAt, &l.OrganizationID)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &l, nil
}

// Cleanup removes links older than the retention horizon (their tickets then
// 404, which is acceptable for year-old emails).
func (r *trackedLinkRepository) Cleanup(ctx context.Context, olderThanDays int) (int64, error) {
	query := `
		DELETE FROM tracked_links
		WHERE created_at < NOW() - $1 * INTERVAL '1 day'
	`

	tag, err := r.db.Exec(ctx, query, olderThanDays)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
