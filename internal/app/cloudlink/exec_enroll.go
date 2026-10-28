package cloudlink

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

var (
	ErrSelfExecutionUnavailable = errx.NewWithIdentifier(errx.Conflict, "cloud_link_self_execution_unavailable", "This Warmbly Cloud cannot leave credentials on your server yet. Upgrade Warmbly Cloud, or let Cloud hold the credential.")
	ErrSelfExecutionNotGranted  = errx.NewWithIdentifier(errx.Forbidden, "cloud_link_self_execution_not_granted", "Keeping credentials on this server needs approval from Warmbly for your Cloud workspace. Ask Warmbly support to enable it.")
	ErrSelfExecutionManaged     = errx.NewWithIdentifier(errx.Unprocessable, "cloud_link_self_execution_managed", "This mailbox signs in through Warmbly Cloud, so its only credential already lives there.")
)

// selfExecutionTTL bounds how stale the advertised grant may be before Cloud is asked again.
const selfExecutionTTL = 5 * time.Minute

type cachedSelfExecution struct {
	info *models.PoolLinkSelfExecution
	at   time.Time
}

// ExecutionWirer lets the composition root hand the service its command store.
type ExecutionWirer interface {
	WireExecution(repository.CloudLinkExecRepository)
}

func (s *service) WireExecution(r repository.CloudLinkExecRepository) { s.exec = r }

func (s *service) rememberSelfExecution(instanceID uuid.UUID, info *models.PoolLinkSelfExecution) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.selfExe == nil {
		s.selfExe = map[uuid.UUID]cachedSelfExecution{}
	}
	s.selfExe[instanceID] = cachedSelfExecution{info: info, at: time.Now()}
}

// selfExecution is the Cloud's advertised grant for a link, nil from a Cloud too old to have one.
func (s *service) selfExecution(ctx context.Context, l *models.CloudLink, fresh bool) (*models.PoolLinkSelfExecution, *errx.Error) {
	if !fresh {
		s.mu.Lock()
		c, ok := s.selfExe[l.InstanceID]
		s.mu.Unlock()
		if ok && time.Since(c.at) < selfExecutionTTL {
			return c.info, nil
		}
	}
	var info models.PoolLinkInstanceInfo
	if xerr := s.clientFor(l).do(ctx, http.MethodGet, "/instance", nil, &info); xerr != nil {
		return nil, xerr
	}
	s.rememberSelfExecution(l.InstanceID, info.SelfExecution)
	return info.SelfExecution, nil
}

// selfExecutionSupported is the gate on every route of the command protocol.
func selfExecutionSupported(info *models.PoolLinkSelfExecution) bool {
	return info != nil && info.Protocol >= models.PoolLinkSelfExecutionProtocol
}

func (s *service) requireSelfExecution(ctx context.Context, l *models.CloudLink) *errx.Error {
	if s.exec == nil {
		return ErrSelfExecutionUnavailable
	}
	info, xerr := s.selfExecution(ctx, l, true)
	if xerr != nil {
		return xerr
	}
	if !selfExecutionSupported(info) {
		return ErrSelfExecutionUnavailable
	}
	if !info.Granted || (info.ExpiresAt != nil && !time.Now().Before(*info.ExpiresAt)) {
		return ErrSelfExecutionNotGranted
	}
	return nil
}
