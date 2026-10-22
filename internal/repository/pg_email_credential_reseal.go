package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
	"github.com/warmbly/warmbly/internal/app/cipher"
	"github.com/warmbly/warmbly/internal/app/mailcred"
	"github.com/warmbly/warmbly/internal/infrastructure/db"
)

// MailboxCredentialResealer moves stored mailbox credentials onto their organization's key.
type MailboxCredentialResealer interface {
	// ResealMailboxCredentials reseals up to limit mailboxes after the cursor and returns the next cursor, uuid.Nil when done.
	ResealMailboxCredentials(ctx context.Context, after uuid.UUID, limit int) (next uuid.UUID, resealed, failed int, err error)
}

const resealCandidatesQuery = `
	SELECT ea.id, ea.organization_id
	FROM email_accounts ea
	WHERE ea.id > $1 AND ea.organization_id IS NOT NULL
	  AND (EXISTS (SELECT 1 FROM email_accounts_smtp_imap s WHERE s.email_account_id = ea.id
	         AND NOT (s.smtp_host LIKE 'dek1:%' AND s.smtp_user LIKE 'dek1:%' AND s.smtp_password LIKE 'dek1:%'
	              AND s.imap_host LIKE 'dek1:%' AND s.imap_user LIKE 'dek1:%' AND s.imap_password LIKE 'dek1:%'))
	    OR EXISTS (SELECT 1 FROM email_accounts_oauth o WHERE o.email_account_id = ea.id
	         AND NOT (o.access_token LIKE 'dek1:%' AND o.refresh_token LIKE 'dek1:%')))
	ORDER BY ea.id
	LIMIT $2`

func (r *emailRepository) ResealMailboxCredentials(ctx context.Context, after uuid.UUID, limit int) (uuid.UUID, int, int, error) {
	if !r.Creds.SealsForOrganizations() {
		return uuid.Nil, 0, 0, nil
	}
	rows, err := r.DB.Query(ctx, resealCandidatesQuery, after, limit)
	if err != nil {
		db.CaptureError(err, resealCandidatesQuery, nil, "query")
		return uuid.Nil, 0, 0, err
	}
	type candidate struct{ id, org uuid.UUID }
	var batch []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.org); err != nil {
			rows.Close()
			return uuid.Nil, 0, 0, err
		}
		batch = append(batch, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return uuid.Nil, 0, 0, err
	}

	resealed, failed := 0, 0
	for _, c := range batch {
		if err := r.resealMailbox(ctx, c.id, c.org); err != nil {
			if ctx.Err() != nil {
				return uuid.Nil, resealed, failed, ctx.Err()
			}
			log.Warn().Err(err).Str("email_account_id", c.id.String()).Str("organization_id", c.org.String()).
				Msg("mailbox credentials: could not move a mailbox onto its organization key; retrying next pass")
			failed++
			continue
		}
		resealed++
	}
	if len(batch) < limit {
		return uuid.Nil, resealed, failed, nil
	}
	return batch[len(batch)-1].id, resealed, failed, nil
}

// resealMailbox rewrites one mailbox's credentials under its organization key, holding the rows so a concurrent refresh waits.
func (r *emailRepository) resealMailbox(ctx context.Context, id, orgID uuid.UUID) error {
	// The key is resolved before any row is locked, so a KMS lookup never holds them.
	c, err := r.Creds.Cipher(ctx, orgID)
	if err != nil {
		return err
	}
	tx, err := r.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// OAuth before SMTP/IMAP, the order ConvertGoogleToAppPassword takes them in.
	var tokens [2]string
	err = tx.QueryRow(ctx, `SELECT access_token, refresh_token FROM email_accounts_oauth WHERE email_account_id = $1 FOR UPDATE`, id).
		Scan(&tokens[0], &tokens[1])
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return err
	default:
		if out, changed, err := r.resealValues(ctx, orgID, c, tokens[:], true); err != nil {
			return err
		} else if changed {
			if _, err := tx.Exec(ctx, `UPDATE email_accounts_oauth SET access_token = $2, refresh_token = $3 WHERE email_account_id = $1`,
				id, out[0], out[1]); err != nil {
				return err
			}
		}
	}

	var smtp [6]string
	err = tx.QueryRow(ctx, `SELECT smtp_host, smtp_user, smtp_password, imap_host, imap_user, imap_password
		FROM email_accounts_smtp_imap WHERE email_account_id = $1 FOR UPDATE`, id).
		Scan(&smtp[0], &smtp[1], &smtp[2], &smtp[3], &smtp[4], &smtp[5])
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return err
	default:
		if out, changed, err := r.resealValues(ctx, orgID, c, smtp[:], false); err != nil {
			return err
		} else if changed {
			if _, err := tx.Exec(ctx, `UPDATE email_accounts_smtp_imap
				SET smtp_host = $2, smtp_user = $3, smtp_password = $4, imap_host = $5, imap_user = $6, imap_password = $7
				WHERE email_account_id = $1`, id, out[0], out[1], out[2], out[3], out[4], out[5]); err != nil {
				return err
			}
		}
	}

	return tx.Commit(ctx)
}

// resealValues opens each value not yet under the organization key and seals it there.
func (r *emailRepository) resealValues(ctx context.Context, orgID uuid.UUID, c *cipher.Cipher, stored []string, legacyPlaintext bool) ([]string, bool, error) {
	out := make([]string, len(stored))
	changed := false
	for i, v := range stored {
		if mailcred.IsOrgSealed(v) {
			out[i] = v
			continue
		}
		if v == "" {
			// An empty value is not ciphertext; the bare prefix is its organization form.
			out[i], changed = mailcred.OrgPrefix, true
			continue
		}
		var plain string
		var err error
		if legacyPlaintext {
			plain, _, err = r.Creds.OpenOrLegacy(ctx, &orgID, v)
		} else {
			plain, err = r.Creds.Open(ctx, &orgID, v)
		}
		if err != nil {
			return nil, false, err
		}
		if out[i], err = mailcred.SealOrg(ctx, c, plain); err != nil {
			return nil, false, err
		}
		changed = true
	}
	return out, changed, nil
}
