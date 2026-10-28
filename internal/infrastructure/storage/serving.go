package storage

import (
	"mime"
	"path"
	"strings"

	"github.com/warmbly/warmbly/internal/pkg/imagetype"
)

// Download names how a signed GET is answered when a browser follows it: the
// file name to save as and the type the object was stored with.
type Download struct {
	Filename    string
	ContentType string
}

// octetStream is the type of every object whose stored type is not safe to
// hand a browser.
const octetStream = "application/octet-stream"

// DownloadType is the Content-Type a stored object is handed over with: its
// bare media type, or octet-stream for anything a browser could run as a
// document or script.
func DownloadType(contentType string) string {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil || mt == "" {
		return octetStream
	}
	switch {
	case strings.Contains(mt, "html"), strings.Contains(mt, "xml"), strings.Contains(mt, "svg"),
		strings.Contains(mt, "javascript"), strings.Contains(mt, "ecmascript"),
		mt == "text/css":
		return octetStream
	}
	return mt
}

// DownloadDisposition is an attachment Content-Disposition naming filename.
func DownloadDisposition(filename string) string {
	name := strings.TrimSpace(path.Base(strings.ReplaceAll(filename, "\\", "/")))
	if name == "" || name == "." || name == "/" {
		return "attachment"
	}
	if v := mime.FormatMediaType("attachment", map[string]string{"filename": name}); v != "" {
		return v
	}
	return "attachment"
}

// PublicHeaders is the Content-Type and Content-Disposition a public object is
// stored with: a raster image inline, anything else as a download.
func PublicHeaders(contentType string) (ct, disposition string) {
	if imagetype.Inline(contentType) {
		mt, _, _ := mime.ParseMediaType(contentType)
		return mt, "inline"
	}
	return octetStream, "attachment"
}
