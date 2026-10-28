// Proposed test. Target: internal/api/handler/public_object_headers_test.go
package handler

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/warmbly/warmbly/internal/infrastructure/storage"
)

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// A raster public key renders inline; any other key is a nosniff, sandboxed octet-stream download.
func TestServePublicObjectSetsProtectiveHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := storage.NewFilesystem(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.Put(ctx, "avatars/u1/a.png", bytes.NewReader(tinyPNG(t)), "image/png"); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, "avatars/u1/a.html", strings.NewReader("<script>alert(1)</script>"), "text/html"); err != nil {
		t.Fatal(err)
	}
	h := &Handler{Storage: store}
	r := gin.New()
	r.GET("/public/*key", h.ServePublicObject)
	for _, tc := range []struct {
		key, ctype, disposition string
	}{
		{"avatars/u1/a.png", "image/png", "inline"},
		{"avatars/u1/a.html", "application/octet-stream", "attachment"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/public/"+tc.key, nil))
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d", w.Code)
			}
			if got := w.Header().Get("Content-Type"); got != tc.ctype {
				t.Fatalf("Content-Type = %q, want %q", got, tc.ctype)
			}
			if got := w.Header().Get("Content-Disposition"); got != tc.disposition {
				t.Fatalf("Content-Disposition = %q, want %q", got, tc.disposition)
			}
			if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Fatalf("X-Content-Type-Options = %q", got)
			}
			if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") || !strings.Contains(csp, "default-src 'none'") {
				t.Fatalf("Content-Security-Policy = %q", csp)
			}
		})
	}
}

// The declared type and filename take no part: content that is not a decodable PNG or JPEG is refused.
func TestReadImageUploadJudgesTheBytes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upload := func(name, declared string, body []byte) (string, string, bool) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		hdr := textproto.MIMEHeader{}
		hdr.Set("Content-Disposition", `form-data; name="file"; filename="`+name+`"`)
		hdr.Set("Content-Type", declared)
		part, err := mw.CreatePart(hdr)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write(body)
		_ = mw.Close()
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/upload", &buf)
		c.Request.Header.Set("Content-Type", mw.FormDataContentType())
		_, mime, ext, e := readImageUpload(c, avatarLimits)
		return mime, ext, e == nil
	}
	good := tinyPNG(t)
	for name, tc := range map[string]struct {
		file, declared string
		body           []byte
	}{
		"html named png":       {"a.png", "image/png", []byte("<html><script>alert(1)</script></html>")},
		"svg declared png":     {"a.png", "image/png", []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`)},
		"fake webp with html":  {"a.webp", "image/webp", append([]byte("RIFF\x24\x00\x00\x00WEBPVP8 "), []byte("<script>alert(1)</script>")...)},
		"truncated png":        {"a.png", "image/png", good[:12]},
		"png bytes, webp name": {"a.webp", "image/webp", append(append([]byte{}, good[:12]...), []byte("<script>")...)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, ok := upload(tc.file, tc.declared, tc.body); ok {
				t.Fatal("disguised content was accepted")
			}
		})
	}
	t.Run("real png declared as html", func(t *testing.T) {
		mime, ext, ok := upload("a.html", "text/html", good)
		if !ok || mime != "image/png" || ext != ".png" {
			t.Fatalf("got %q %q %v, want image/png .png accepted", mime, ext, ok)
		}
	})
}
