// Workspace image library for email bodies (issue #380) — upload, list, delete.
//
// Unlike an attachment, an image placed in the body is fetched by the
// recipient's mail client with no session of ours, so the bytes are written as
// a public object and the row keeps the URL the composer inserts. The same
// per-plan storage quota that bounds attachments bounds these, under the same
// lock, because both live in the same object store.

package handler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/api/middleware"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/infrastructure/storage"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/pkg/imagetype"
	"github.com/warmbly/warmbly/internal/utils/paging"
)

const (
	// Per-image cap. An email body image has to load on a phone over a slow
	// connection, so this is deliberately far below the attachment ceiling.
	emailImageMaxBytes int64 = 5 * 1024 * 1024
	// Dimension backstop against a raw camera dump; the composer downscales
	// before uploading, so this only catches a bypass of that path.
	emailImageMaxDimension = 4000
	// Default page size, and the ceiling a caller may ask for.
	emailImageListLimit = 40
	emailImageListMax   = 100
)

// Formats every mail client renders, decided from the bytes alone. SVG is not
// one: it is a script-capable document and no mail client renders it.
var allowedEmailImageFormats = imagetype.Raster

// UploadEmailImage — POST /email-images (multipart "file")
func (h *Handler) UploadEmailImage(c *gin.Context) {
	orgID := middleware.GetOrganizationID(c)
	if orgID == nil {
		errx.JSON(c, errx.New(errx.BadRequest, "no organization selected"))
		return
	}
	if h.Storage == nil {
		errx.JSON(c, errx.New(errx.ServiceUnavailable, "object storage not configured"))
		return
	}
	if h.EmailImageRepo == nil {
		errx.JSON(c, errx.New(errx.ServiceUnavailable, "image library not available"))
		return
	}

	// Cap the body before the first form field is read: that read parses the
	// whole multipart payload.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, emailImageMaxBytes+(1<<20))

	fh, err := c.FormFile("file")
	if err != nil {
		errx.JSON(c, errx.New(errx.BadRequest, "file is required"))
		return
	}
	if fh.Size <= 0 {
		errx.JSON(c, emailImageLimits.unreadable())
		return
	}
	if fh.Size > emailImageMaxBytes {
		errx.JSON(c, emailImageLimits.tooLarge())
		return
	}

	src, err := fh.Open()
	if err != nil {
		errx.JSON(c, errx.InternalError())
		return
	}
	defer src.Close()
	buf := &bytes.Buffer{}
	if _, err := io.Copy(buf, src); err != nil {
		errx.JSON(c, errx.InternalError())
		return
	}
	body := buf.Bytes()

	// The bytes decide, never the client-declared type: this object is served
	// back from our own public origin.
	format, cfg, err := imagetype.Detect(body, allowedEmailImageFormats...)
	if errors.Is(err, imagetype.ErrUnreadable) {
		errx.JSON(c, emailImageLimits.unreadable())
		return
	}
	if err != nil {
		errx.JSON(c, emailImageLimits.wrongFormat())
		return
	}
	mimeType, ext := format.MIME, format.Ext
	if cfg.Width > emailImageMaxDimension || cfg.Height > emailImageMaxDimension {
		errx.JSON(c, emailImageLimits.tooWide())
		return
	}

	filename := emailImageFilename(fh.Filename, ext)
	key := models.EmailImageObjectKey(*orgID, ext)
	stored, xerr := putPublicObject(c.Request.Context(), h.Storage, key, body, mimeType)
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	url := absolutePublicURL(c, stored)

	img := &models.EmailImage{
		OrganizationID: *orgID,
		Filename:       filename,
		MimeType:       mimeType,
		Size:           fh.Size,
		Width:          cfg.Width,
		Height:         cfg.Height,
		StorageKey:     key,
		URL:            url,
	}
	if uid, perr := uuid.Parse(middleware.GetUserID(c)); perr == nil {
		img.UserID = &uid
	}

	// The row is the reservation: written only if the org's total still fits
	// once this image is counted, with the limit re-read under the lock. A
	// refused image is removed from storage again, on a context that outlives
	// the request so a cancelled upload cannot strand the object.
	limitFn := func(ctx context.Context) (int64, error) {
		l, xerr := h.FeatureGateService.GetStorageLimitBytes(ctx, *orgID)
		if xerr != nil {
			return 0, xerr
		}
		return l, nil
	}
	created, used, limit, err := h.EmailImageRepo.CreateWithinQuota(c.Request.Context(), img, limitFn)
	if err != nil {
		h.deleteObjectDetached(c.Request.Context(), key)
		errx.JSON(c, errx.InternalError())
		return
	}
	if !created {
		h.deleteObjectDetached(c.Request.Context(), key)
		errx.JSON(c, errx.StorageLimitReached(used, limit, fh.Size))
		return
	}

	h.auditOrg(c, models.AuditActionCreate, models.AuditEntityEmailImage, &img.ID, nil, map[string]string{
		"filename": filename,
	})

	c.JSON(http.StatusCreated, img)
}

// ListEmailImages — GET /email-images
func (h *Handler) ListEmailImages(c *gin.Context) {
	orgID := middleware.GetOrganizationID(c)
	if orgID == nil {
		errx.JSON(c, errx.New(errx.BadRequest, "no organization selected"))
		return
	}
	if h.EmailImageRepo == nil {
		c.JSON(http.StatusOK, gin.H{"data": []models.EmailImage{}, "pagination": gin.H{"next_cursor": nil, "has_more": false}})
		return
	}
	limit := emailImageListLimit
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > emailImageListMax {
			errx.JSON(c, errx.New(errx.BadRequest, fmt.Sprintf("limit must be between 1 and %d", emailImageListMax)))
			return
		}
		limit = n
	}
	beforeAt, beforeID, xerr := paging.DecodeTimeCursor(c.Query("cursor"))
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}

	// One extra row answers "is there a next page" without a second count.
	imgs, err := h.EmailImageRepo.ListByOrg(c.Request.Context(), *orgID, limit+1, beforeAt, beforeID)
	if err != nil {
		errx.JSON(c, errx.InternalError())
		return
	}
	var nextCursor *string
	if len(imgs) > limit {
		last := imgs[limit-1]
		nextCursor = paging.EncodeTime(last.CreatedAt, last.ID)
		imgs = imgs[:limit]
	}
	c.JSON(http.StatusOK, gin.H{
		"data": imgs,
		"pagination": gin.H{
			"next_cursor": nextCursor,
			"has_more":    nextCursor != nil,
		},
	})
}

// DeleteEmailImage — DELETE /email-images/:id. The bytes go with the row, so
// any email already sent with this image loses it; the dashboard says so first.
func (h *Handler) DeleteEmailImage(c *gin.Context) {
	orgID := middleware.GetOrganizationID(c)
	if orgID == nil {
		errx.JSON(c, errx.New(errx.BadRequest, "no organization selected"))
		return
	}
	if h.EmailImageRepo == nil {
		errx.JSON(c, errx.New(errx.ServiceUnavailable, "image library not available"))
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errx.JSON(c, errx.ErrUuid)
		return
	}
	img, err := h.EmailImageRepo.GetByID(c.Request.Context(), id)
	if err != nil {
		errx.JSON(c, errx.InternalError())
		return
	}
	// The route id is a raw path parameter, so ownership is proved here or an
	// image of another workspace could be deleted.
	if img == nil || img.OrganizationID != *orgID {
		errx.JSON(c, errx.ErrNotFound)
		return
	}
	// The object goes first. Deleting the row first and then failing here would
	// leave a public URL that still loads while nothing counts its bytes
	// against the quota and nothing remembers the key. This way a failure
	// changes nothing and the caller can retry; the delete is idempotent, so a
	// retry after the object is already gone still removes the row.
	if h.Storage != nil {
		if err := h.Storage.Delete(c.Request.Context(), img.StorageKey); err != nil && !errors.Is(err, storage.ErrNotFound) {
			errx.JSON(c, errx.New(errx.ServiceUnavailable, "the image could not be removed from storage; try again"))
			return
		}
	}
	if err := h.EmailImageRepo.Delete(c.Request.Context(), id); err != nil {
		errx.JSON(c, errx.InternalError())
		return
	}
	h.auditOrg(c, models.AuditActionDelete, models.AuditEntityEmailImage, &id, nil, map[string]string{
		"filename": img.Filename,
	})
	c.Status(http.StatusNoContent)
}

// absolutePublicURL makes a public object URL loadable from outside this
// deployment. The filesystem backend returns a path when BLOB_PUBLIC_BASE_URL
// is unset, and a recipient's mail client cannot resolve a path, so it is
// anchored to this API's own origin.
func absolutePublicURL(c *gin.Context, stored string) string {
	if stored == "" || strings.HasPrefix(stored, "http://") || strings.HasPrefix(stored, "https://") {
		return stored
	}
	return publicAPIBaseURL(c) + "/" + strings.TrimPrefix(stored, "/")
}

// emailImageFilename is the display name kept on the row: what the library
// lists and what the alt text defaults to. The extension is forced to match
// the sniffed type so the name never claims to be something the bytes are not.
// It never reaches an object key, which is generated independently.
func emailImageFilename(raw, ext string) string {
	name := sanitizeFilename(raw)
	name = strings.TrimSpace(strings.TrimSuffix(name, path.Ext(name)))
	if name == "" {
		name = "image"
	}
	return name + ext
}
