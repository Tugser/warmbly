package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/api/middleware"
	"github.com/warmbly/warmbly/internal/app/poollink"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

// poolLinkSelfExecution resolves the calling instance and the self-execution service, answering when either is missing.
func (h *Handler) poolLinkSelfExecution(c *gin.Context) (*models.PoolLinkInstance, poollink.SelfExecutionService, bool) {
	inst := middleware.GetPoolLinkInstance(c)
	if inst == nil {
		errx.JSON(c, errx.ErrUnauthorized)
		return nil, nil, false
	}
	svc, ok := h.PoolLinkService.(poollink.SelfExecutionService)
	if !ok {
		errx.JSON(c, poollink.ErrSelfExecutionUnavailable)
		return nil, nil, false
	}
	return inst, svc, true
}

// PoolLinkLeaseCommands hands the instance the commands due for mailboxes it executes.
func (h *Handler) PoolLinkLeaseCommands(c *gin.Context) {
	inst, svc, ok := h.poolLinkSelfExecution(c)
	if !ok {
		return
	}
	var req models.PoolLinkLeaseRequest
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			errx.JSON(c, errx.InvalidBody(err))
			return
		}
	}
	out, xerr := svc.LeaseCommands(c.Request.Context(), inst, req)
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	c.JSON(http.StatusOK, out)
}

// PoolLinkCommandResult records the instance's answer to one command; an identical replay is accepted again.
func (h *Handler) PoolLinkCommandResult(c *gin.Context) {
	inst, svc, ok := h.poolLinkSelfExecution(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errx.JSON(c, errx.ErrUuid)
		return
	}
	var r models.PoolLinkCommandResult
	if err := c.ShouldBindJSON(&r); err != nil {
		errx.JSON(c, errx.InvalidBody(err))
		return
	}
	if xerr := svc.AnswerCommand(c.Request.Context(), inst, id, r); xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	c.Status(http.StatusNoContent)
}

// PoolLinkWarmupArrival takes warmup mail the instance found in a mailbox it executes.
func (h *Handler) PoolLinkWarmupArrival(c *gin.Context) {
	inst, svc, ok := h.poolLinkSelfExecution(c)
	if !ok {
		return
	}
	remoteID, ok := poolLinkRemoteID(c)
	if !ok {
		return
	}
	var a models.PoolLinkWarmupArrival
	if err := c.ShouldBindJSON(&a); err != nil {
		errx.JSON(c, errx.InvalidBody(err))
		return
	}
	verdict, xerr := svc.ReportArrival(c.Request.Context(), inst, remoteID, a)
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	c.JSON(http.StatusOK, verdict)
}

// PoolLinkWarmupObservation takes a later change to warmup mail Cloud accepted.
func (h *Handler) PoolLinkWarmupObservation(c *gin.Context) {
	inst, svc, ok := h.poolLinkSelfExecution(c)
	if !ok {
		return
	}
	remoteID, ok := poolLinkRemoteID(c)
	if !ok {
		return
	}
	var o models.PoolLinkWarmupObservation
	if err := c.ShouldBindJSON(&o); err != nil {
		errx.JSON(c, errx.InvalidBody(err))
		return
	}
	if xerr := svc.ReportObservation(c.Request.Context(), inst, remoteID, o); xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	c.Status(http.StatusNoContent)
}

// AdminListOrgSelfExecutedMailboxes is each instance-executed mailbox of a workspace with its reporter standing.
func (h *Handler) AdminListOrgSelfExecutedMailboxes(c *gin.Context) {
	orgID, ok := adminOrgParam(c)
	if !ok {
		return
	}
	svc, ok := h.PoolLinkService.(poollink.SelfExecutionService)
	if !ok {
		c.JSON(http.StatusOK, []models.PoolLinkSelfExecutionMailbox{})
		return
	}
	out, xerr := svc.ListSelfExecutionMailboxes(c.Request.Context(), orgID)
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	c.JSON(http.StatusOK, out)
}
