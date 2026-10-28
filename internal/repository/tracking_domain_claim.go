package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/infrastructure/db"
)

// ErrTrackingDomainTaken: another organization holds the tracking host verified.
var ErrTrackingDomainTaken = errors.New("tracking domain verified by another organization")

// trackingDomainConstraint is the name the verification trigger (migration 000258) raises under.
const trackingDomainConstraint = "tracking_domain_one_organization"

func isTrackingDomainTaken(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == trackingDomainConstraint
}

// trackingDomainHeldElsewhere reports whether an organization other than orgID
// has host verified on a mailbox, a campaign or as its forms domain.
func trackingDomainHeldElsewhere(ctx context.Context, d *db.DB, orgID any, host string) (bool, *errx.Error) {
	if host == "" {
		return false, nil
	}
	query := `
		SELECT EXISTS (
			SELECT 1 FROM email_accounts
			WHERE tracking_domain = $1 AND tracking_domain_verified
			  AND organization_id IS DISTINCT FROM $2::uuid
			UNION ALL
			SELECT 1 FROM campaigns
			WHERE tracking_domain = $1 AND tracking_domain_verified
			  AND organization_id IS DISTINCT FROM $2::uuid
			UNION ALL
			SELECT 1 FROM organizations
			WHERE forms_domain = $1 AND forms_domain_verified
			  AND id IS DISTINCT FROM $2::uuid
		)`
	var held bool
	if err := d.QueryRow(ctx, query, host, orgID).Scan(&held); err != nil {
		db.CaptureError(err, query, nil, "queryrow")
		return false, errx.InternalError()
	}
	return held, nil
}
