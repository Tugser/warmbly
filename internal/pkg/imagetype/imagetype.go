// Package imagetype decides what an uploaded image is from its bytes alone.
//
// An image is accepted only when the content sniff and that format's own
// header decoder agree on it, and only from the allowlist the caller passes.
// The declared MIME type and the filename never take part.
package imagetype

import (
	"bytes"
	"errors"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"strings"

	"golang.org/x/image/webp"
)

// Format is one raster format an upload may be.
type Format struct {
	// MIME is the type the object is stored and served with.
	MIME string
	// Ext is the extension its object key carries.
	Ext    string
	config func(io.Reader) (image.Config, error)
}

var (
	PNG  = Format{MIME: "image/png", Ext: ".png", config: png.DecodeConfig}
	JPEG = Format{MIME: "image/jpeg", Ext: ".jpg", config: jpeg.DecodeConfig}
	GIF  = Format{MIME: "image/gif", Ext: ".gif", config: gif.DecodeConfig}
	WebP = Format{MIME: "image/webp", Ext: ".webp", config: webp.DecodeConfig}
)

// Raster is every format an upload handler can store under a public key.
var Raster = []Format{PNG, JPEG, GIF, WebP}

var (
	// ErrNotAllowed is content that is not one of the allowed formats.
	ErrNotAllowed = errors.New("imagetype: not an allowed image format")
	// ErrUnreadable is content that sniffs as an allowed format but whose
	// header does not decode as it.
	ErrUnreadable = errors.New("imagetype: image could not be parsed")
)

// Detect returns body's format and dimensions when it is one of allowed.
func Detect(body []byte, allowed ...Format) (Format, image.Config, error) {
	sniffed := http.DetectContentType(body)
	for _, f := range allowed {
		if f.MIME != sniffed {
			continue
		}
		cfg, err := f.config(bytes.NewReader(body))
		if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
			return Format{}, image.Config{}, ErrUnreadable
		}
		return f, cfg, nil
	}
	return Format{}, image.Config{}, ErrNotAllowed
}

// ByExt is the raster format an object key's extension names.
func ByExt(ext string) (Format, bool) {
	switch strings.ToLower(ext) {
	case ".png":
		return PNG, true
	case ".jpg", ".jpeg":
		return JPEG, true
	case ".gif":
		return GIF, true
	case ".webp":
		return WebP, true
	}
	return Format{}, false
}

// Inline reports whether contentType is a raster type a browser may render
// inline from one of our origins. Everything else, SVG included, is a download.
func Inline(contentType string) bool {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	for _, f := range Raster {
		if f.MIME == ct {
			return true
		}
	}
	return false
}
