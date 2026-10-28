package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/api/middleware"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

// GetEmailCertificates reads an SMTP/IMAP mailbox's certificate policy per server.
// GET /emails/:id/certificates
func (h *Handler) GetEmailCertificates(c *gin.Context) {
	orgID := middleware.GetOrganizationID(c)
	if orgID == nil {
		errx.Handle(c, errx.ErrNoOrganization)
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errx.Handle(c, errx.ErrUuid)
		return
	}
	out, xerr := h.EmailService.MailboxCertificates(c.Request.Context(), orgID, id)
	if xerr != nil {
		errx.Handle(c, xerr)
		return
	}
	c.JSON(http.StatusOK, out)
}

// CheckEmailCertificates runs a connection test and reports the certificate
// each server presents now. It changes nothing, so a retry is safe.
// POST /emails/onboarding/smtp-imap/:id/certificates/check
func (h *Handler) CheckEmailCertificates(c *gin.Context) {
	orgID := middleware.GetOrganizationID(c)
	if orgID == nil {
		errx.Handle(c, errx.ErrNoOrganization)
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errx.Handle(c, errx.ErrUuid)
		return
	}
	out, xerr := h.EmailService.CheckMailboxCertificates(c.Request.Context(), orgID, id)
	if xerr != nil {
		errx.Handle(c, xerr)
		return
	}
	c.JSON(http.StatusOK, out)
}

// ConfirmEmailCertificates pins the fingerprints the owner confirmed. The body
// is the desired pins, so a retry converges on the same stored value.
// POST /emails/onboarding/smtp-imap/:id/certificates/confirm
func (h *Handler) ConfirmEmailCertificates(c *gin.Context) {
	orgID := middleware.GetOrganizationID(c)
	if orgID == nil {
		errx.Handle(c, errx.ErrNoOrganization)
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errx.Handle(c, errx.ErrUuid)
		return
	}
	var req models.ConfirmMailboxCertificates
	if err := c.ShouldBindJSON(&req); err != nil {
		errx.Handle(c, errx.InvalidBody(err))
		return
	}
	out, acc, xerr := h.EmailService.ConfirmMailboxCertificates(c.Request.Context(), orgID, id, req)
	if xerr != nil {
		errx.Handle(c, xerr)
		return
	}
	h.auditOrg(c, models.AuditActionUpdate, models.AuditEntityEmailAccount, &acc.ID, map[string]string{
		"smtp_tls_pin": out.SMTP.TLSPin,
		"imap_tls_pin": out.IMAP.TLSPin,
	}, map[string]string{
		"provider": "smtp_imap",
		"email":    acc.Email,
		"change":   "tls_certificate_confirmed",
	})
	c.JSON(http.StatusOK, out)
}
