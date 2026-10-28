package jobs

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

type actionTargetRepoFake struct {
	repository.PoolLinkRepository
	executed bool
}

func (f actionTargetRepoFake) InstanceExecuted(context.Context, uuid.UUID) (*repository.PoolLinkExecMailbox, error) {
	if !f.executed {
		return nil, nil
	}
	return &repository.PoolLinkExecMailbox{}, nil
}

type actionTargetExecFake struct {
	actionTargetRepoFake
	repository.PoolLinkExecRepository
}

func (f actionTargetExecFake) InstanceExecuted(ctx context.Context, id uuid.UUID) (*repository.PoolLinkExecMailbox, error) {
	return f.actionTargetRepoFake.InstanceExecuted(ctx, id)
}

func TestWarmupActionWorkerFollowsTheExecutor(t *testing.T) {
	ctx := context.Background()
	worker := uuid.New()
	stale := &models.Email{ID: uuid.New(), WorkerID: &worker}

	s := &JobsService{PoolLinkRepo: actionTargetExecFake{actionTargetRepoFake: actionTargetRepoFake{executed: true}}}
	if got, ok := s.warmupActionWorker(ctx, stale); !ok || got != uuid.Nil {
		t.Fatalf("instance-executed mailbox with a stale worker = %v, %v", got, ok)
	}
	s.PoolLinkRepo = actionTargetExecFake{}
	if got, ok := s.warmupActionWorker(ctx, stale); !ok || got != worker {
		t.Fatalf("Cloud-executed mailbox = %v, %v", got, ok)
	}
	if _, ok := s.warmupActionWorker(ctx, &models.Email{ID: uuid.New()}); ok {
		t.Fatal("a mailbox with no worker and no executor must not be routable")
	}
}
