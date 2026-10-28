package jobs

import (
	"context"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// warmupActionWorker is where a mailbox's warmup actions are published: the
// nil worker that queues them for the linked instance executing it, else its
// worker. False when neither can act.
func (s *JobsService) warmupActionWorker(ctx context.Context, account *models.Email) (uuid.UUID, bool) {
	if account == nil {
		return uuid.Nil, false
	}
	if exec, ok := s.PoolLinkRepo.(repository.PoolLinkExecRepository); ok {
		m, err := exec.InstanceExecuted(ctx, account.ID)
		if err != nil {
			return uuid.Nil, false
		}
		if m != nil {
			return uuid.Nil, true
		}
	}
	if account.WorkerID != nil {
		return *account.WorkerID, true
	}
	return uuid.Nil, false
}
