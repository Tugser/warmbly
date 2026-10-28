package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/warmbly/warmbly/internal/infrastructure/storage"
)

// The public object route serves one stored object or nothing: a directory, a prefix or a key outside the public set is a 404 with no body.
func TestServePublicObjectNeverListsADirectory(t *testing.T) {
	store, err := storage.NewFilesystem(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, key := range []string{"avatars/u1/a.png", "form-assets/f1/logo.png", "private/secret.txt"} {
		if err := store.Put(ctx, key, bytes.NewReader([]byte("\x89PNG")), "image/png"); err != nil {
			t.Fatal(err)
		}
	}
	h := &Handler{Storage: store}
	r := gin.New()
	r.GET("/public/*key", h.ServePublicObject)
	for _, tc := range []struct {
		path string
		code int
	}{
		{"/public/", http.StatusNotFound},
		{"/public/avatars", http.StatusNotFound},
		{"/public/avatars/", http.StatusNotFound},
		{"/public/avatars/u1", http.StatusNotFound},
		{"/public/avatars/u1/", http.StatusNotFound},
		{"/public/form-assets/", http.StatusNotFound},
		{"/public/private/secret.txt", http.StatusNotFound},
		{"/public/avatars/../private/secret.txt", http.StatusNotFound},
		{"/public/avatars/u1/a.png", http.StatusOK},
	} {
		t.Run(tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if w.Code != tc.code {
				t.Fatalf("status = %d, want %d", w.Code, tc.code)
			}
			if tc.code == http.StatusNotFound && w.Body.Len() != 0 {
				t.Fatalf("a refused path answered with a body: %q", w.Body.String())
			}
		})
	}
}
