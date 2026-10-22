package jobs

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/warmbly/warmbly/internal/jobrun"
	"github.com/warmbly/warmbly/internal/repository"
)

const (
	// mailboxResealBatch bounds how many mailboxes one transaction-per-mailbox batch touches.
	mailboxResealBatch = 100
	// mailboxResealPassEvery spaces complete passes; new writes are already sealed, so a pass only finds stragglers.
	mailboxResealPassEvery = 6 * time.Hour
)

// StartMailboxCredentialReseal moves stored mailbox credentials onto their organization's key, resuming from where it stopped.
func (s *JobsService) StartMailboxCredentialReseal(ctx context.Context) {
	resealer, ok := s.EmailRepository.(repository.MailboxCredentialResealer)
	if !ok {
		return
	}
	var cursor uuid.UUID
	var nextPass time.Time
	resealed, failed := 0, 0
	jobrun.Loop(ctx, "mailbox_credential_reseal", time.Minute, true, func(ctx context.Context) error {
		if time.Now().Before(nextPass) {
			return nil
		}
		batchCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		next, r, f, err := resealer.ResealMailboxCredentials(batchCtx, cursor, mailboxResealBatch)
		resealed, failed = resealed+r, failed+f
		if err != nil {
			return err
		}
		cursor = next
		if cursor != uuid.Nil {
			return nil
		}
		if resealed > 0 || failed > 0 {
			log.Info().Int("resealed", resealed).Int("unreadable", failed).Msg("mailbox credentials: pass moved stored credentials onto organization keys")
		}
		resealed, failed = 0, 0
		nextPass = time.Now().Add(mailboxResealPassEvery)
		return nil
	})
}
