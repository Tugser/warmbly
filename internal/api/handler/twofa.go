package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/twofa"
	"github.com/warmbly/warmbly/internal/errx"
)

// withCodeBudget runs a signed-in code check under the reauth failure budget,
// so a session cannot guess its way to a valid code on these routes either.
func (h *Handler) withCodeBudget(c *gin.Context, uid uuid.UUID, check func() *errx.Error) *errx.Error {
	ctx := c.Request.Context()
	if ok, xerr := h.AuthService.ReserveReauthAttempt(ctx, uid); xerr != nil {
		return xerr
	} else if !ok {
		return errx.ErrAuthLimit
	}
	xerr := check()
	switch {
	case xerr == nil:
		h.AuthService.ClearReauthFailures(ctx, uid)
	case xerr.Identifier != twofa.InvalidCodeID:
		// No code was compared (2FA off, server error), so nothing was guessed.
		h.AuthService.ReleaseReauthAttempt(ctx, uid)
	}
	return xerr
}

// TwoFAStatus reports whether the caller has 2FA enabled, since when, and how
// many recovery codes are left.
func (h *Handler) TwoFAStatus(c *gin.Context) {
	uid, ok := notifActor(c)
	if !ok {
		return
	}
	status, err := h.TwoFAService.Status(c.Request.Context(), uid)
	if err != nil {
		errx.JSON(c, errx.InternalError())
		return
	}
	c.JSON(http.StatusOK, status)
}

// TwoFARegenerateRecoveryCodes replaces the caller's recovery codes (requires a
// current TOTP or recovery code) and returns the new set once.
func (h *Handler) TwoFARegenerateRecoveryCodes(c *gin.Context) {
	uid, ok := notifActor(c)
	if !ok {
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		errx.JSON(c, errx.InvalidBody(err))
		return
	}
	var codes []string
	xerr := h.withCodeBudget(c, uid, func() *errx.Error {
		var e *errx.Error
		codes, e = h.TwoFAService.RegenerateRecoveryCodes(c.Request.Context(), uid, body.Code)
		return e
	})
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	c.JSON(http.StatusOK, gin.H{"recovery_codes": codes})
}

// TwoFAEnrollStart begins enrollment, returning the secret + otpauth URI once.
func (h *Handler) TwoFAEnrollStart(c *gin.Context) {
	uid, ok := notifActor(c)
	if !ok {
		return
	}
	if !h.allowEnrollment(c, uid) {
		return
	}
	res, xerr := h.TwoFAService.EnrollStart(c.Request.Context(), uid)
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	c.JSON(http.StatusOK, res)
}

// TwoFAEnrollConfirm verifies a code, enables 2FA, and returns recovery codes once.
func (h *Handler) TwoFAEnrollConfirm(c *gin.Context) {
	uid, ok := notifActor(c)
	if !ok {
		return
	}
	if !h.allowEnrollment(c, uid) {
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		errx.JSON(c, errx.InvalidBody(err))
		return
	}
	codes, xerr := h.TwoFAService.EnrollConfirm(c.Request.Context(), uid, body.Code)
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	c.JSON(http.StatusOK, gin.H{"recovery_codes": codes})
}

// TwoFADisable turns off 2FA (requires a current TOTP or recovery code).
func (h *Handler) TwoFADisable(c *gin.Context) {
	uid, ok := notifActor(c)
	if !ok {
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	_ = c.ShouldBindJSON(&body)
	xerr := h.withCodeBudget(c, uid, func() *errx.Error {
		return h.TwoFAService.Disable(c.Request.Context(), uid, body.Code)
	})
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// TwoFAVerifyLogin (PUBLIC) exchanges a pending token + code for a real session.
func (h *Handler) TwoFAVerifyLogin(c *gin.Context) {
	var body struct {
		PendingToken string `json:"pending_token"`
		Code         string `json:"code"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		errx.JSON(c, errx.InvalidBody(err))
		return
	}
	tok, xerr := h.TwoFAService.VerifyLogin(c.Request.Context(), body.PendingToken, body.Code, c.ClientIP(), c.Request.UserAgent())
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	c.JSON(http.StatusOK, tok)
}
