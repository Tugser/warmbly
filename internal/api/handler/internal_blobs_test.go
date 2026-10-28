package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	zlog "github.com/rs/zerolog/log"

	"github.com/warmbly/warmbly/internal/infrastructure/storage"
)

// The presign broker signs only node prefixes, refuses traversal, and logs every refusal.
func TestPresignRefusesForeignPrefixAndLogsIt(t *testing.T) {
	var buf bytes.Buffer
	prev := zlog.Logger
	zlog.Logger = zerolog.New(&buf)
	defer func() { zlog.Logger = prev }()

	fs, err := storage.NewFilesystem(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{Storage: fs}
	r := gin.New()
	r.POST("/presign", h.InternalPresignBlob)
	refused := []string{"avatars/users/a.png", "exports/org.zip", "emails/../avatars/x", "users/../../form-assets/x"}
	for _, key := range refused {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/presign", strings.NewReader(`{"op":"get","key":"`+key+`"}`)))
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s: status %d, want 403", key, w.Code)
		}
	}
	out := buf.String()
	if strings.Count(out, `"event":"broker_access"`) != len(refused) || strings.Count(out, `"allowed":false`) != len(refused) {
		t.Fatalf("refusals not logged: %s", out)
	}

	// A node prefix passes the gate; the filesystem backend then declines to sign.
	buf.Reset()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/presign", strings.NewReader(`{"op":"get","key":"emails/abc"}`)))
	if w.Code != http.StatusNotImplemented || !strings.Contains(buf.String(), `"allowed":true`) {
		t.Fatalf("node prefix: status %d, log %s", w.Code, buf.String())
	}
}
