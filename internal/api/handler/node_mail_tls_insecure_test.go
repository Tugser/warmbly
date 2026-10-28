package handler

import (
	"testing"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
)

// A node is handed the MAIL_TLS_INSECURE decision the control plane made, so a hosted node never skips certificate checks.
func TestRenderNodeEnvResolvesMailTLSInsecurePolicy(t *testing.T) {
	setInstanceEnv(t)
	for _, tc := range []struct {
		name, mode, appEnv, flag, want string
	}{
		{"hosted production ignores it", "cloud", "prod", "true", "false"},
		{"hosted production default", "cloud", "prod", "", "false"},
		{"self-host honours it", "self_hosted", "prod", "true", "true"},
		{"self-host default", "self_hosted", "prod", "", "false"},
		{"development honours it", "cloud", "dev", "true", "true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DEPLOYMENT_MODE", tc.mode)
			t.Setenv("APP_ENV", tc.appEnv)
			t.Setenv("MAIL_TLS_INSECURE", tc.flag)
			env := envLines(t, renderNodeEnv(uuid.New(), models.NodeRoleWorker, ""))
			if env["MAIL_TLS_INSECURE"] != tc.want {
				t.Errorf("MAIL_TLS_INSECURE = %q, want %q", env["MAIL_TLS_INSECURE"], tc.want)
			}
		})
	}
}
