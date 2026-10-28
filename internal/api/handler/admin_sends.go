// Admin endpoints for the send outcome loop and the task queues: in-flight
// reservations, dead letters, task failures and customer webhook delivery
// health across every workspace.

package handler

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/config"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

const (
	deadLetterEntity models.AuditEntityType = "task_dead_letter"
	sendHoldEntity   models.AuditEntityType = "send_recovery_hold"
)

// webhookDeliveryLease mirrors the default LeaseTimeout in
// webhook.NewDeliveryWorker (cmd/backend wires the worker without overriding it).
const webhookDeliveryLease = 5 * time.Minute

// parseBoundedLimit is parseLimit with a page cap above the admin default of 100.
func parseBoundedLimit(s string, def, max int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

func (h *Handler) adminSendsReady(c *gin.Context) bool {
	if h.AdminSendsRepo == nil {
		errx.JSON(c, errx.New(errx.NotImplemented, "send operations are not available on this instance"))
		return false
	}
	return true
}

// AdminInFlightSends lists reserved sends no worker result has resolved yet.
func (h *Handler) AdminInFlightSends(c *gin.Context) {
	if !h.adminSendsReady(c) {
		return
	}
	limit := parseBoundedLimit(c.Query("limit"), 100, 500)
	reclaimAfter := time.Duration(config.CampaignSendReclaimAfterMinutes) * time.Minute
	result, err := h.AdminSendsRepo.InFlight(c.Request.Context(), reclaimAfter, limit)
	if err != nil {
		errx.JSON(c, errx.New(errx.Internal, err.Error()))
		return
	}
	c.JSON(http.StatusOK, result)
}

// AdminListDeadLetters pages task dead letters newest first.
func (h *Handler) AdminListDeadLetters(c *gin.Context) {
	if !h.adminSendsReady(c) {
		return
	}
	cursor := parseCursor(c.Query("cursor"))
	limit := parseLimit(c.Query("limit"), 50)
	status := c.Query("status")
	switch status {
	case "", "pending", "replayed", "failed":
	default:
		errx.JSON(c, errx.New(errx.BadRequest, "invalid status"))
		return
	}
	result, err := h.AdminSendsRepo.ListDeadLetters(c.Request.Context(), status, cursor, limit)
	if err != nil {
		errx.JSON(c, errx.New(errx.Internal, err.Error()))
		return
	}
	c.JSON(http.StatusOK, result)
}

// AdminReplayDeadLetter re-enqueues one dead letter through its workspace.
func (h *Handler) AdminReplayDeadLetter(c *gin.Context) {
	if !h.adminSendsReady(c) {
		return
	}
	if h.AdvancedService == nil {
		errx.JSON(c, errx.New(errx.NotImplemented, "dead letter replay is not available on this instance"))
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errx.JSON(c, errx.New(errx.BadRequest, "invalid dead letter ID"))
		return
	}
	row, err := h.AdminSendsRepo.GetDeadLetter(c.Request.Context(), id)
	if err != nil {
		errx.JSON(c, errx.New(errx.Internal, err.Error()))
		return
	}
	if row == nil {
		errx.JSON(c, errx.New(errx.NotFound, "dead letter not found"))
		return
	}
	if row.OrganizationID == nil {
		errx.JSON(c, errx.New(errx.BadRequest, "dead letter has no organization; its task or mailbox is gone"))
		return
	}
	if xerr := h.AdvancedService.ReplayDeadLetter(c.Request.Context(), *row.OrganizationID, id); xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	h.audit(c, models.AuditActionResume, deadLetterEntity, &id, map[string]string{
		"organization_id": row.OrganizationID.String(),
		"task_id":         row.TaskID.String(),
		"task_type":       row.TaskType,
	})
	c.JSON(http.StatusOK, gin.H{"replayed": true})
}

// AdminSendHolds lists mailboxes held from sending and what holds each one.
func (h *Handler) AdminSendHolds(c *gin.Context) {
	if !h.adminSendsReady(c) {
		return
	}
	result, err := h.AdminSendsRepo.SendHolds(c.Request.Context(), parseBoundedLimit(c.Query("limit"), 100, 500))
	if err != nil {
		errx.JSON(c, errx.InternalError())
		return
	}
	c.JSON(http.StatusOK, result)
}

// AdminResolveSendHold records an operator's provider-checked verdict on a held send through the mailbox's own resolution path.
func (h *Handler) AdminResolveSendHold(c *gin.Context) {
	if !h.adminSendsReady(c) {
		return
	}
	if h.EmailService == nil {
		errx.JSON(c, errx.New(errx.NotImplemented, "send hold resolution is not available on this instance"))
		return
	}
	mailboxID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errx.JSON(c, errx.New(errx.BadRequest, "invalid mailbox ID"))
		return
	}
	var body models.AdminResolveSendHold
	if err := c.ShouldBindJSON(&body); err != nil {
		errx.JSON(c, errx.New(errx.BadRequest, "invalid request body"))
		return
	}
	body.ConfirmationReference = strings.TrimSpace(body.ConfirmationReference)
	body.MessageID = strings.TrimSpace(body.MessageID)
	if body.ConfirmationReference == "" || strings.ContainsAny(body.ConfirmationReference, "\r\n") {
		errx.JSON(c, errx.New(errx.BadRequest, "confirmation_reference must be one non-empty line"))
		return
	}
	if body.Outcome == "sent" && body.MessageID == "" {
		errx.JSON(c, errx.New(errx.BadRequest, "message_id is required when confirming a send went out"))
		return
	}
	resolution, ok := body.Resolution()
	if !ok {
		errx.JSON(c, errx.New(errx.BadRequest, "outcome does not apply to this hold reason"))
		return
	}
	held, err := h.AdminSendsRepo.GetSendHold(c.Request.Context(), mailboxID)
	if err != nil {
		errx.JSON(c, errx.InternalError())
		return
	}
	if held == nil {
		errx.JSON(c, errx.NewWithIdentifier(errx.NotFound, "send_hold_not_found", "This mailbox is not held from sending."))
		return
	}
	if held.OrganizationID == nil || held.HeldTaskID == nil || *held.HeldTaskID != body.HeldTaskID || held.Reason != body.HeldReason {
		errx.JSON(c, errx.NewWithIdentifier(errx.Conflict, "send_hold_changed", "This hold has changed since it was loaded. Refresh and check it again."))
		return
	}
	if !held.Resolvable {
		errx.JSON(c, errx.NewWithIdentifier(errx.Conflict, "send_hold_not_resolvable", "This hold cannot be resolved right now (blocker: "+held.Blocker+"). The Holds list says what it is waiting for."))
		return
	}
	update := &models.UpdateEmail{SendRecoveryResolution: resolution}
	if _, xerr := h.EmailService.Update(c.Request.Context(), held.OrganizationID.String(), "", mailboxID.String(), update); xerr != nil {
		if xerr == errx.ErrInvalid {
			errx.JSON(c, errx.NewWithIdentifier(errx.Conflict, "send_hold_not_resolvable", "The hold or its send changed and the evidence no longer applies. Refresh and check it again."))
			return
		}
		errx.JSON(c, xerr)
		return
	}
	h.audit(c, models.AuditActionUpdate, sendHoldEntity, &mailboxID, map[string]string{
		"organization_id": held.OrganizationID.String(),
		"held_task_id":    body.HeldTaskID.String(),
		"held_reason":     body.HeldReason,
		"outcome":         body.Outcome,
	})
	// An unknown verdict is applied by the result consumer, so the hold lifts shortly after, not now.
	c.JSON(http.StatusOK, gin.H{"accepted": true, "applied_by_consumer": body.HeldReason == "unknown"})
}

// AdminRecentTaskFailures lists the newest task failures with their mailbox.
func (h *Handler) AdminRecentTaskFailures(c *gin.Context) {
	if !h.adminSendsReady(c) {
		return
	}
	limit := parseBoundedLimit(c.Query("limit"), 100, 500)
	rows, err := h.AdminSendsRepo.RecentTaskFailures(c.Request.Context(), limit)
	if err != nil {
		errx.JSON(c, errx.New(errx.Internal, err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": rows})
}

// AdminWebhookHealth is the instance-wide webhook delivery picture.
func (h *Handler) AdminWebhookHealth(c *gin.Context) {
	if !h.adminSendsReady(c) {
		return
	}
	health, err := h.AdminSendsRepo.WebhookHealth(c.Request.Context(), webhookDeliveryLease)
	if err != nil {
		errx.JSON(c, errx.New(errx.Internal, err.Error()))
		return
	}
	c.JSON(http.StatusOK, health)
}

// AdminWebhookReclaim re-queues deliveries stranded in_flight past the lease.
func (h *Handler) AdminWebhookReclaim(c *gin.Context) {
	if h.WebhookRepo == nil {
		errx.JSON(c, errx.New(errx.NotImplemented, "webhook delivery is not available on this instance"))
		return
	}
	n, err := h.WebhookRepo.ReclaimStuckDeliveries(c.Request.Context(), webhookDeliveryLease)
	if err != nil {
		errx.JSON(c, errx.New(errx.Internal, err.Error()))
		return
	}
	h.audit(c, models.AuditActionUpdate, models.AuditEntityWebhook, nil, map[string]string{
		"action":    "reclaim_stuck_deliveries",
		"reclaimed": strconv.FormatInt(n, 10),
	})
	c.JSON(http.StatusOK, gin.H{"reclaimed": n})
}

// AdminListOrgWebhooks lists one workspace's endpoints with recent counts.
func (h *Handler) AdminListOrgWebhooks(c *gin.Context) {
	if !h.adminSendsReady(c) {
		return
	}
	orgID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errx.JSON(c, errx.New(errx.BadRequest, "invalid organization ID"))
		return
	}
	rows, err := h.AdminSendsRepo.OrgWebhooks(c.Request.Context(), orgID)
	if err != nil {
		errx.JSON(c, errx.New(errx.Internal, err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": rows})
}
