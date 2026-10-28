package orgtransfer

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

func TestPublicImageWritesOnlyBytesMatchingTheKeyExtension(t *testing.T) {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	pngBody := b.Bytes()

	if body, ct, ok := publicImage(bytes.NewReader(pngBody), "form-assets/o/logo.png"); !ok || ct != "image/png" || !bytes.Equal(body, pngBody) {
		t.Fatalf("png under .png: ok=%v ct=%q", ok, ct)
	}
	if _, _, ok := publicImage(bytes.NewReader(pngBody), "form-assets/o/logo.jpg"); ok {
		t.Fatal("png under .jpg accepted")
	}
	if _, _, ok := publicImage(bytes.NewReader([]byte("<html><script>1</script></html>")), "avatars/users/a.png"); ok {
		t.Fatal("markup under .png accepted")
	}
	if _, _, ok := publicImage(bytes.NewReader([]byte("RIFF\x30\x00\x00\x00WEBPVP8 <html>")), "email-images/o/a.webp"); ok {
		t.Fatal("undecodable webp accepted")
	}
	if _, _, ok := publicImage(bytes.NewReader(pngBody), "avatars/users/a.svg"); ok {
		t.Fatal("svg key accepted")
	}
}
