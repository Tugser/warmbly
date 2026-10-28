package config

import (
	"strings"
	"testing"
)

// The backend refuses to boot with an AUTH_SECRET shorter than the HS256 floor.
func TestLoadAuthConfigRefusesAShortAuthSecret(t *testing.T) {
	t.Setenv("AWS_CONFIG_ENABLED", "")
	for _, n := range []int{1, 16, MinAuthSecretLength - 1} {
		t.Setenv("AUTH_SECRET", strings.Repeat("a", n))
		if _, err := (&Config{}).LoadAuthConfig(t.Context()); err == nil {
			t.Fatalf("AUTH_SECRET of %d bytes was accepted", n)
		}
	}
	t.Setenv("AUTH_SECRET", strings.Repeat("a", MinAuthSecretLength))
	if _, err := (&Config{}).LoadAuthConfig(t.Context()); err != nil && strings.Contains(err.Error(), "AUTH_SECRET must be") {
		t.Fatalf("a %d-byte AUTH_SECRET was refused: %v", MinAuthSecretLength, err)
	}
}
