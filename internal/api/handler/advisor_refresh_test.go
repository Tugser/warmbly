package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/api/middleware"
	"github.com/warmbly/warmbly/internal/app/advisor"
	"github.com/warmbly/warmbly/internal/app/audit"
	"github.com/warmbly/warmbly/internal/app/sequence"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

type editedSequenceService struct {
	sequence.SequenceService
	step models.Sequence
}

func (s *editedSequenceService) Update(_ context.Context, _, _, _ string, _ *models.UpdateSequence) (*models.Sequence, *errx.Error) {
	return &s.step, nil
}

type editAdvisorEvaluator struct {
	advisor.Service
	refreshed chan uuid.UUID
}

func (s *editAdvisorEvaluator) Evaluate(_ context.Context, orgID uuid.UUID, _ string) (*models.AdvisorSummary, error) {
	s.refreshed <- orgID
	return &models.AdvisorSummary{}, nil
}

func TestSavingSequenceRefreshesAdvisorForItsOrganization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID, userID, campaignID, stepID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	evaluator := &editAdvisorEvaluator{refreshed: make(chan uuid.UUID, 1)}
	h := &Handler{
		SequenceService: &editedSequenceService{step: models.Sequence{ID: stepID}},
		AuditService:    audit.NewNoOpService(), AdvisorService: evaluator,
	}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(middleware.OrganizationIDKey, orgID)
		c.Set(middleware.UserIDKey, userID.String())
	})
	r.PATCH("/campaigns/:id/sequences/:sid", h.UpdateSequence)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/campaigns/"+campaignID.String()+"/sequences/"+stepID.String(), strings.NewReader(`{"wait_after":3}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("save returned %d: %s", w.Code, w.Body.String())
	}
	select {
	case got := <-evaluator.refreshed:
		if got != orgID {
			t.Fatalf("refreshed organization %s, want %s", got, orgID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("saving a step did not refresh its advice")
	}
}
