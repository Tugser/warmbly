package config

import (
	"context"
	"testing"

	"github.com/gin-gonic/gin"
)

// Production must run gin in release mode without anyone remembering GIN_MODE.
func TestGinModeFollowsAppEnv(t *testing.T) {
	t.Setenv("WEBSOCKET_URL", "wss://realtime.example.com/socket")
	t.Setenv("GIN_MODE", "")
	for env, want := range map[string]string{"prod": gin.ReleaseMode, "dev": gin.DebugMode} {
		api, err := (&Config{Env: env}).LoadApiConfig(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if api.GinMode != want {
			t.Errorf("APP_ENV=%s: GinMode = %q, want %q", env, api.GinMode, want)
		}
	}
}
