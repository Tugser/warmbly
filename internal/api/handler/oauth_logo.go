package handler

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/api/middleware"
	"github.com/warmbly/warmbly/internal/app/oauth"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/infrastructure/storage"
	"github.com/warmbly/warmbly/internal/models"
)

// An app logo is shown to other workspaces on the consent screen and in the
// directory, so it is only ever an image this instance re-encoded and stored
// for the app's own workspace.

const (
	appLogoPrefix  = oauth.AppLogoPrefix
	appLogoMinSide = 32
	// ErrCodeInvalidLogo is the response code for a logo URL this instance did not issue.
	ErrCodeInvalidLogo = "invalid_logo"
)

// reencodeLogo decodes the upload and writes it out again, so what is stored
// is pixels only: no metadata (camera, location), no trailing bytes another
// parser could read as something else.
func reencodeLogo(body []byte) ([]byte, string, string, *errx.Error) {
	img, format, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return nil, "", "", errx.New(errx.BadRequest, "image could not be parsed")
	}
	b := img.Bounds()
	if b.Dx() < appLogoMinSide || b.Dy() < appLogoMinSide {
		return nil, "", "", errx.New(errx.BadRequest, "the logo must be at least 32px on each side")
	}
	var out bytes.Buffer
	switch format {
	case "png":
		if err := png.Encode(&out, img); err != nil {
			return nil, "", "", errx.InternalError()
		}
		return out.Bytes(), "image/png", ".png", nil
	case "jpeg":
		if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: 90}); err != nil {
			return nil, "", "", errx.InternalError()
		}
		return out.Bytes(), "image/jpeg", ".jpg", nil
	}
	return nil, "", "", errx.New(errx.BadRequest, "the logo must be a PNG or JPG")
}

// appOwnLogoPrefix is the key prefix of images stored for one app. Only those
// are deleted when its logo changes; an older upload another app may share is left alone.
func appOwnLogoPrefix(appID uuid.UUID) string {
	return appLogoPrefix + appID.String() + "-"
}

// deleteAppLogo removes an app's previous image when it was stored for that app,
// or is a workspace upload that no other app of the workspace still shows.
func (h *Handler) deleteAppLogo(ctx context.Context, orgID, appID uuid.UUID, previousURL, keepKey string) {
	if previousURL == "" {
		return
	}
	if avatarKeyFromURL(previousURL, appOwnLogoPrefix(appID)) != "" {
		h.deleteAvatarObject(ctx, previousURL, appOwnLogoPrefix(appID), keepKey)
		return
	}
	orgPrefix := appLogoPrefix + orgID.String() + "/"
	if avatarKeyFromURL(previousURL, orgPrefix) == "" {
		return
	}
	apps, err := h.OAuthService.ListApplications(ctx, orgID)
	if err != nil {
		return
	}
	for _, a := range apps {
		if a.ID != appID && a.LogoURL == previousURL {
			return
		}
	}
	h.deleteAvatarObject(ctx, previousURL, orgPrefix, keepKey)
}

func appLogoKey(orgID uuid.UUID, ext string) (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return appLogoPrefix + orgID.String() + "/" + hex.EncodeToString(buf) + ext, nil
}

// checkAppLogo accepts an empty logo, the app's current one, or a logo this
// instance stored for this workspace. Anything else (another workspace's
// upload, an outside URL that could track who opens the consent screen) is
// refused.
func checkAppLogo(ctx context.Context, store storage.Store, orgID uuid.UUID, logoURL, current string) *errx.Error {
	logoURL = strings.TrimSpace(logoURL)
	if logoURL == "" || logoURL == current {
		return nil
	}
	refuse := errx.NewWithIdentifier(errx.BadRequest, ErrCodeInvalidLogo, "upload the logo through Warmbly; other image addresses are not accepted")
	pu, ok := store.(storage.PublicURLer)
	if !ok || store == nil {
		return refuse
	}
	key, ok := oauth.IssuedLogoKey(pu, orgID, logoURL)
	if !ok {
		return refuse
	}
	r, err := store.Get(ctx, key)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return refuse
		}
		return errx.New(errx.Internal, "logo lookup failed")
	}
	_ = r.Close()
	return nil
}

// hostedAppLogo reports whether logoURL is an app logo this instance stored, read without a fetch.
func hostedAppLogo(store storage.Store, logoURL string) bool {
	pu, ok := store.(storage.PublicURLer)
	if !ok || store == nil || logoURL == "" {
		return false
	}
	base := pu.PublicURL("")
	if !strings.HasPrefix(logoURL, base) {
		return false
	}
	key := strings.TrimPrefix(logoURL, base)
	if !strings.HasPrefix(key, appLogoPrefix) || strings.Contains(key, "..") || pu.PublicURL(key) != logoURL {
		return false
	}
	return strings.HasSuffix(key, ".png") || strings.HasSuffix(key, ".jpg")
}

// appLogoTarget resolves the workspace and the app it owns for a logo change.
func (h *Handler) appLogoTarget(c *gin.Context) (uuid.UUID, *models.OAuthApplication, bool) {
	orgID := middleware.GetOrganizationID(c)
	if orgID == nil {
		errx.JSON(c, errx.New(errx.BadRequest, "no organization selected"))
		return uuid.Nil, nil, false
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errx.JSON(c, errx.New(errx.BadRequest, "invalid id"))
		return uuid.Nil, nil, false
	}
	app, gerr := h.OAuthService.GetApplication(c.Request.Context(), *orgID, id)
	if gerr != nil {
		errx.JSON(c, errx.New(errx.Internal, "lookup failed"))
		return uuid.Nil, nil, false
	}
	if app == nil {
		errx.JSON(c, errx.New(errx.NotFound, "application not found"))
		return uuid.Nil, nil, false
	}
	return *orgID, app, true
}

// UploadOAuthApplicationLogo is POST /oauth/applications/:id/logo, the same
// shape as the workspace logo: the server stores the image and sets it on the
// app, so no client ever supplies an image address, and the previous image is
// deleted once nothing points at it.
func (h *Handler) UploadOAuthApplicationLogo(c *gin.Context) {
	orgID, app, ok := h.appLogoTarget(c)
	if !ok {
		return
	}
	if app.SuspendedAt != nil {
		errx.JSON(c, oauthAppWriteError(oauth.ErrAppSuspended))
		return
	}
	userID, _ := middleware.GetUserUUID(c)
	if err := h.OAuthService.CheckDeveloperAccess(c.Request.Context(), orgID, userID); err != nil {
		errx.JSON(c, oauthAppWriteError(err))
		return
	}
	raw, _, _, xerr := readAvatarUpload(c)
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	body, mime, ext, xerr := reencodeLogo(raw)
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	ctx := c.Request.Context()
	key := avatarObjectKey(appLogoPrefix, app.ID, ext)
	url, xerr := putPublicObject(ctx, h.Storage, key, body, mime)
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	updated, err := h.OAuthService.SetLogo(ctx, orgID, app.ID, url)
	if err != nil {
		_ = h.Storage.Delete(ctx, key)
		errx.JSON(c, errx.InternalError())
		return
	}
	h.deleteAppLogo(ctx, orgID, app.ID, app.LogoURL, key)
	h.auditOrg(c, models.AuditActionUpdate, models.AuditEntityOAuthApplication, &app.ID, nil, map[string]string{"logo": "uploaded"})
	c.JSON(http.StatusOK, updated)
}

// DeleteOAuthApplicationLogo is DELETE /oauth/applications/:id/logo.
func (h *Handler) DeleteOAuthApplicationLogo(c *gin.Context) {
	orgID, app, ok := h.appLogoTarget(c)
	if !ok {
		return
	}
	if app.SuspendedAt != nil {
		errx.JSON(c, oauthAppWriteError(oauth.ErrAppSuspended))
		return
	}
	ctx := c.Request.Context()
	updated, err := h.OAuthService.SetLogo(ctx, orgID, app.ID, "")
	if err != nil {
		errx.JSON(c, errx.InternalError())
		return
	}
	h.deleteAppLogo(ctx, orgID, app.ID, app.LogoURL, "")
	h.auditOrg(c, models.AuditActionUpdate, models.AuditEntityOAuthApplication, &app.ID, nil, map[string]string{"logo": "removed"})
	c.JSON(http.StatusOK, updated)
}
