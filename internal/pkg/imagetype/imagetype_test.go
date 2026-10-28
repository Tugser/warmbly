package imagetype

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
)

// A 1x1 lossless WebP.
const tinyWebP = "UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA=="

func encoded(t *testing.T, enc func(*bytes.Buffer, image.Image) error) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	img.Set(1, 1, color.RGBA{R: 200, A: 255})
	var b bytes.Buffer
	if err := enc(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestDetectAcceptsEachRasterFromItsBytes(t *testing.T) {
	webp, err := base64.StdEncoding.DecodeString(tinyWebP)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		body []byte
		want Format
	}{
		{"png", encoded(t, func(b *bytes.Buffer, i image.Image) error { return png.Encode(b, i) }), PNG},
		{"jpeg", encoded(t, func(b *bytes.Buffer, i image.Image) error { return jpeg.Encode(b, i, nil) }), JPEG},
		{"gif", encoded(t, func(b *bytes.Buffer, i image.Image) error { return gif.Encode(b, i, nil) }), GIF},
		{"webp", webp, WebP},
	}
	for _, tc := range cases {
		got, cfg, err := Detect(tc.body, Raster...)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got.MIME != tc.want.MIME || got.Ext != tc.want.Ext {
			t.Fatalf("%s: detected %s", tc.name, got.MIME)
		}
		if cfg.Width <= 0 || cfg.Height <= 0 {
			t.Fatalf("%s: no dimensions", tc.name)
		}
	}
}

func TestDetectRefusesContentThatDoesNotDecode(t *testing.T) {
	// A RIFF/WEBP signature sniffs as WebP; the payload behind it is markup.
	fake := append([]byte("RIFF\x30\x00\x00\x00WEBPVP8 "), []byte("<html><script>alert(1)</script></html>")...)
	if _, _, err := Detect(fake, Raster...); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("fake webp: got %v", err)
	}
	truncatedPNG := encoded(t, func(b *bytes.Buffer, i image.Image) error { return png.Encode(b, i) })[:12]
	if _, _, err := Detect(truncatedPNG, Raster...); err == nil {
		t.Fatal("truncated png accepted")
	}
}

func TestDetectRefusesFormatsOutsideTheAllowlist(t *testing.T) {
	gifBody := encoded(t, func(b *bytes.Buffer, i image.Image) error { return gif.Encode(b, i, nil) })
	if _, _, err := Detect(gifBody, PNG, JPEG); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("gif with png/jpeg allowed: got %v", err)
	}
	for name, body := range map[string][]byte{
		"svg":  []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		"html": []byte("<!DOCTYPE html><html><body>x</body></html>"),
		"text": []byte("just text"),
	} {
		if _, _, err := Detect(body, Raster...); !errors.Is(err, ErrNotAllowed) {
			t.Fatalf("%s: got %v", name, err)
		}
	}
}

func TestInlineIsRasterOnly(t *testing.T) {
	for ct, want := range map[string]bool{
		"image/png":                true,
		"IMAGE/JPEG":               true,
		"image/webp; q=1":          true,
		"image/gif":                true,
		"image/svg+xml":            false,
		"text/html":                false,
		"application/octet-stream": false,
		"":                         false,
	} {
		if got := Inline(ct); got != want {
			t.Fatalf("Inline(%q) = %v", ct, got)
		}
	}
}
