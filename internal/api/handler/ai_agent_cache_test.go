package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSSEEmitterDoesNotStoreEvents(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/ai/agent", nil)
	sseEmitter[writingEvent](c)
	if got := w.Header().Get("Cache-Control"); got != "no-cache, no-store" {
		t.Errorf("Cache-Control = %q, want no-cache, no-store", got)
	}
	if !w.Flushed {
		t.Error("event stream should flush immediately")
	}
}
