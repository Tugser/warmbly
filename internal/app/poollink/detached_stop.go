package poollink

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/repository"
)

// DetachedStopRollout keeps the pass repeating long enough to catch a revoke an older replica handled during the rollout.
const DetachedStopRollout = 30 * time.Minute

// StopDetachedWarmup stops Cloud warmup on mailboxes still warming after their link was revoked, then never runs again.
func (s *service) StopDetachedWarmup(ctx context.Context) error {
	r, ok := s.repo.(repository.PoolLinkDetachedRepository)
	if !ok || s.emailSvc == nil {
		return nil
	}
	started := time.Now()
	st, err := r.DetachedWarmupStopState(ctx, started)
	if err != nil {
		return err
	}
	if st.DoneAt != nil {
		return nil
	}
	mailboxes, err := r.ListDetachedWarmupMailboxes(ctx)
	if err != nil {
		return err
	}
	stopped, failed := 0, 0
	for _, m := range mailboxes {
		if _, xerr := s.emailSvc.SetWarmupLifecycle(ctx, m.OrganizationID.String(), m.EmailAccountID.String(), "stop"); xerr != nil && xerr != errx.ErrNotFound {
			log.Warn().Str("account_id", m.EmailAccountID.String()).Str("code", xerr.Identifier).Msg("pool link: warmup of a revoked link's mailbox could not be stopped; retrying next pass")
			failed++
			continue
		}
		log.Info().Str("account_id", m.EmailAccountID.String()).Msg("pool link: stopped warmup of a revoked link's mailbox")
		stopped++
	}
	log.Info().Int("stopped", stopped).Int("failed", failed).Msg("pool link: revoked-link warmup stop pass finished")
	if failed > 0 {
		return fmt.Errorf("pool link: %d revoked-link mailboxes still warming", failed)
	}
	if started.Before(st.FencedAt.Add(DetachedStopRollout)) {
		return nil
	}
	return r.MarkDetachedWarmupStopDone(ctx, started)
}
