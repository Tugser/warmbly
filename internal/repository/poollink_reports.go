package repository

import (
	"context"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
)

func (r *poolLinkRepository) ListReportMailboxes(ctx context.Context, instanceID uuid.UUID, remoteIDs []uuid.UUID) ([]models.PoolLinkMailbox, error) {
	const query = `SELECT instance_id, remote_id, email_account_id, enrolled_at, managed, last_token_at, executor
		FROM pool_link_mailboxes WHERE instance_id = $1 AND remote_id = ANY($2::uuid[])`
	rows, err := r.db.Query(ctx, query, instanceID, remoteIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]models.PoolLinkMailbox, 0, len(remoteIDs))
	for rows.Next() {
		m, err := scanPoolLinkMailbox(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}
