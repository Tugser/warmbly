package seed

import "testing"

func TestDevEnvironmentRequiresAnExplicitDevValue(t *testing.T) {
	for env, allowed := range map[string]bool{
		"": false, "  ": false, "prod": false, "production": false, "staging": false,
		"dev": true, "DEV": true, "development": true, " local ": true,
	} {
		t.Setenv("APP_ENV", env)
		if got := DevEnvironment() == nil; got != allowed {
			t.Errorf("APP_ENV=%q: allowed=%v, want %v", env, got, allowed)
		}
	}
}
