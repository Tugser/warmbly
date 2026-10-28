package handler

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/infrastructure/storage"
	"github.com/warmbly/warmbly/internal/pkg/safehttp"
)

const dashboardImageMaxBytes = 5 << 20

var (
	dashboardImageHTTP  = safehttp.PublicClient(10 * time.Second)
	dashboardImageSlots = make(chan struct{}, 8)
	errDashboardImage   = errors.New("image preview unavailable; use a public image of 5 MB or smaller")
)

func (h *Handler) DashboardImage(c *gin.Context) {
	h.dashboardImage(c, false)
}

// PublicDashboardImage serves only owned public blobs and Slack sign-in avatars.
func (h *Handler) PublicDashboardImage(c *gin.Context) {
	h.dashboardImage(c, true)
}

func (h *Handler) dashboardImage(c *gin.Context, publicOnly bool) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var body struct {
		URL string `json:"url" binding:"required,max=8192"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		errx.Handle(c, errx.InvalidBody(err))
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	select {
	case dashboardImageSlots <- struct{}{}:
		defer func() { <-dashboardImageSlots }()
	case <-ctx.Done():
		errx.JSON(c, errx.New(errx.ServiceUnavailable, "image preview is busy; try again"))
		return
	}
	data, contentType, err := loadDashboardImage(ctx, body.URL, h.Storage, dashboardImageHTTP, publicOnly)
	if err != nil {
		errx.JSON(c, errx.New(errx.BadRequest, errDashboardImage.Error()))
		return
	}
	c.Header("Cache-Control", "no-store")
	// Read by the dashboard's fetch, never rendered as a page of this origin.
	c.Header("Content-Disposition", "attachment")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "default-src 'none'; script-src 'none'; sandbox; frame-ancestors 'none'")
	c.Data(http.StatusOK, contentType, data)
}

func loadDashboardImage(ctx context.Context, raw string, store storage.Store, client *http.Client, publicOnly bool) ([]byte, string, error) {
	var reader io.ReadCloser
	if pu, ok := store.(storage.PublicURLer); ok && pu.PublicURL("") != "" && strings.HasPrefix(raw, pu.PublicURL("")) {
		key := strings.TrimPrefix(raw, pu.PublicURL(""))
		if !isPublicKey(key) || pu.PublicURL(key) != raw {
			return nil, "", errDashboardImage
		}
		var err error
		reader, err = store.Get(ctx, key)
		if err != nil {
			return nil, "", errDashboardImage
		}
	} else {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" {
			return nil, "", errDashboardImage
		}
		if publicOnly && !publicAvatarURL(u) {
			return nil, "", errDashboardImage
		}
		if u.Port() != "" && u.Port() != "443" && u.Port() != "8443" {
			return nil, "", errDashboardImage
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, "", errDashboardImage
		}
		req.Header.Set("Accept", "image/*")
		if publicOnly {
			publicClient := *client
			publicClient.CheckRedirect = func(next *http.Request, via []*http.Request) error {
				if !publicAvatarURL(next.URL) || len(via) >= 5 {
					return errDashboardImage
				}
				if client.CheckRedirect != nil {
					return client.CheckRedirect(next, via)
				}
				return nil
			}
			client = &publicClient
		}
		res, err := client.Do(req)
		if err != nil {
			return nil, "", errDashboardImage
		}
		if res.StatusCode != http.StatusOK || res.ContentLength > dashboardImageMaxBytes {
			_ = res.Body.Close()
			return nil, "", errDashboardImage
		}
		reader = res.Body
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, dashboardImageMaxBytes+1))
	if err != nil || len(data) == 0 || len(data) > dashboardImageMaxBytes {
		return nil, "", errDashboardImage
	}
	contentType := dashboardImageType(data)
	if contentType == "" {
		return nil, "", errDashboardImage
	}
	return data, contentType, nil
}

func publicAvatarURL(u *url.URL) bool {
	return u.Scheme == "https" && u.User == nil && (u.Host == "avatars.slack-edge.com" || (u.Host == "secure.gravatar.com" && strings.HasPrefix(u.Path, "/avatar/")))
}

func dashboardImageType(data []byte) string {
	contentType := http.DetectContentType(data)
	switch contentType {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/x-icon", "image/bmp":
		return contentType
	}
	if len(data) >= 16 && string(data[4:8]) == "ftyp" && (string(data[8:12]) == "avif" || string(data[8:12]) == "avis") {
		return "image/avif"
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	root, depth := false, 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			if root && depth == 0 {
				return "image/svg+xml"
			}
			return ""
		}
		if err != nil {
			return ""
		}
		switch element := token.(type) {
		case xml.StartElement:
			if element.Name.Local == "script" || element.Name.Local == "foreignObject" {
				return ""
			}
			for _, attr := range element.Attr {
				if strings.HasPrefix(strings.ToLower(attr.Name.Local), "on") || (attr.Name.Local == "href" && strings.HasPrefix(strings.ToLower(strings.TrimSpace(attr.Value)), "javascript:")) {
					return ""
				}
			}
			if depth == 0 {
				if root || element.Name.Local != "svg" || element.Name.Space != "http://www.w3.org/2000/svg" {
					return ""
				}
				root = true
			}
			depth++
			if depth > 64 {
				return ""
			}
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth == 0 && len(bytes.TrimSpace(element)) != 0 {
				return ""
			}
		case xml.Directive:
			return ""
		}
	}
}
