package bootstrap

import (
	"bytes"
	"context"
	"log"
	"os"
	"strings"
	"testing"
)

func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	fn()
	return buf.String()
}

// Outside development the boot log names the command that prints a claim link, never a link.
func TestBootLogPointsAtSetupLinkOutsideDevelopment(t *testing.T) {
	for _, env := range []string{"", "prod", "production", "staging"} {
		t.Setenv("APP_ENV", env)
		s := &Service{}
		out := captureLog(t, func() {
			if err := s.printSetupToken(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
		if !strings.Contains(out, "warmblyctl setup-link") || strings.Contains(out, "token=") {
			t.Errorf("APP_ENV=%q: boot log = %q", env, out)
		}
	}
}

// In development the link is printed, so with no cache to hold one the log says why.
func TestBootLogInDevelopmentExplainsAMissingCache(t *testing.T) {
	t.Setenv("APP_ENV", "dev")
	out := captureLog(t, func() {
		if err := (&Service{}).printSetupToken(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "no cache is configured") {
		t.Fatalf("boot log = %q", out)
	}
}
