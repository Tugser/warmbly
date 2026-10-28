package notify

import (
	"bytes"
	"context"
	"log"
	"os"
	"strings"
	"testing"
)

func TestLogTransportKeepsLinkTokensOutOfTheLogOutsideDevelopment(t *testing.T) {
	const body = `<p>Your code is 123456.</p><a href="https://app.example.com/invite?token=s3cret&amp;x=1">https://app.example.com/invite?token=s3cret&amp;x=1</a> <p>https://app.example.com/settings</p>`
	for env, logged := range map[string]bool{"": false, "prod": false, "staging": false, "dev": true, " Development ": true} {
		t.Setenv("APP_ENV", env)
		var buf bytes.Buffer
		log.SetOutput(&buf)
		_ = NewLogEmailNotificationService("Warmbly", "noreply@example.com").Send(context.Background(), []string{"a@example.com"}, nil, nil, "Hi", body)
		log.SetOutput(os.Stderr)
		out := buf.String()
		if got := strings.Contains(out, "s3cret"); got != logged {
			t.Errorf("APP_ENV=%q: token logged = %v, want %v", env, got, logged)
		}
		if !strings.Contains(out, "123456") || !strings.Contains(out, "https://app.example.com/settings") {
			t.Errorf("APP_ENV=%q: the code or a plain link is missing:\n%s", env, out)
		}
		if !logged && !strings.Contains(out, "https://app.example.com/invite?[redacted]") {
			t.Errorf("APP_ENV=%q: the redacted link is missing:\n%s", env, out)
		}
	}
}

func TestLogTransportWithholdsFragmentTokensOutsideDevelopment(t *testing.T) {
	t.Setenv("APP_ENV", "prod")
	var buf bytes.Buffer
	log.SetOutput(&buf)
	_ = NewLogEmailNotificationService("Warmbly", "noreply@example.com").Send(context.Background(), []string{"user@example.com"}, nil, nil, "Reset", `<a href="https://app.example.com/auth/reset-password/confirm#session=fragtok">reset</a>`)
	log.SetOutput(os.Stderr)
	if out := buf.String(); strings.Contains(out, "fragtok") {
		t.Fatalf("fragment token logged:\n%s", out)
	}
}
