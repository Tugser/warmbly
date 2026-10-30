package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/jobrun"
	"github.com/warmbly/warmbly/internal/models"
)

func (s *JobsService) StartWarmupFilingRecovery(ctx context.Context) {
	if s.WarmupRecoveryRepo == nil || s.EmailRepository == nil || s.Publisher == nil {
		return
	}
	jobrun.Loop(ctx, "warmup_filing_recovery", time.Minute, true, func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		return errors.Join(s.retryWarmupFilings(ctx), s.WarmupRecoveryRepo.PurgeExpiredIdentifiers(ctx))
	})
}

func (s *JobsService) retryWarmupFilings(ctx context.Context) error {
	actions, err := s.WarmupRecoveryRepo.ClaimFilings(ctx, 100)
	if err != nil {
		return err
	}
	var failures []error
	for _, action := range actions {
		account, xerr := s.EmailRepository.GetByID(ctx, action.EmailID)
		if xerr != nil {
			if xerr.Code != errx.NotFound {
				failures = append(failures, xerr)
			}
			continue
		}
		workerID, routable := s.warmupActionWorker(ctx, account)
		if !routable {
			continue
		}
		action.Placement, action.TargetFolder = account.WarmupFiling()
		if action.Placement == models.WarmupPlacementInbox {
			id, err := uuid.Parse(action.FilingID)
			if err != nil {
				failures = append(failures, err)
				continue
			}
			failures = append(failures, s.WarmupRecoveryRepo.CompleteFiling(ctx, action.EmailID, id))
			continue
		}
		if err := s.Publisher.PublishWarmupAction(ctx, workerID, &action); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (s *JobsService) HandleWarmupFiled(ctx context.Context, event *models.JobEventWarmupFiled) error {
	if event == nil || s.WarmupRecoveryRepo == nil {
		return nil
	}
	if err := s.WarmupRecoveryRepo.CompleteFiling(ctx, event.EmailID, event.FilingID); err != nil {
		return fmt.Errorf("complete warmup filing: %w", err)
	}
	return nil
}
