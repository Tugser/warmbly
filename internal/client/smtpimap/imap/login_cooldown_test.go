package imap

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	goimap "github.com/emersion/go-imap/v2"
	"github.com/warmbly/warmbly/internal/errx"
)

func TestLoginLockoutsAreRetryableBeforeCredentialClassification(t *testing.T) {
	for _, code := range []goimap.ResponseCode{"", goimap.ResponseCodeAlert, goimap.ResponseCodeAuthenticationFailed} {
		for _, text := range []string{"LOGIN DENIED -- TOO MANY FAILURES", "Excessive Failures -- IP TEMP BANNED", "Too many login attempts", "Too many authentication failures"} {
			t.Run(string(code)+text, func(t *testing.T) {
				c := &Client{}
				got := c.handleError(&goimap.Error{Type: goimap.StatusResponseTypeNo, Code: code, Text: text})
				if got == nil || got.Code != errx.MailErrorCodeSendingTooFast || got.RetryAfter != loginCooldownDuration || got.ResolveMethod != errx.MailErrorResolveMethodRetry {
					t.Fatalf("lockout must retry automatically: %+v", got)
				}
				if got.Type == errx.MailErrorCritical || strings.Contains(got.Message, text) {
					t.Fatalf("lockout must neither disable the mailbox nor forward provider text: %+v", got)
				}
			})
		}
	}
}

func TestLoginCooldownStopsWireRetriesAndAllowsRecovery(t *testing.T) {
	var recovered atomic.Bool
	c, wire := interceptingServer(t, nil, func(line string) string {
		parts := strings.Fields(line)
		if len(parts) > 1 && strings.EqualFold(parts[1], "LOGIN") && !recovered.Load() {
			return fmt.Sprintf("%s NO [AUTHENTICATIONFAILED] LOGIN DENIED -- TOO MANY FAILURES\r\n", parts[0])
		}
		return ""
	})
	if err := c.Connect(); err == nil || err.Code != errx.MailErrorCodeSendingTooFast {
		t.Fatalf("lockout not recognized: %+v", err)
	}
	deadline := c.loginRetryAt.Load()
	for range 3 {
		if err := c.Connect(); err == nil || err.Code != errx.MailErrorCodeSendingTooFast || err.RetryAfter <= 0 {
			t.Fatalf("cooldown bypassed: %+v", err)
		}
		if _, err := c.ListFolders(); err == nil || err.Code != errx.MailErrorCodeSendingTooFast {
			t.Fatalf("command reconnect bypassed cooldown: %+v", err)
		}
	}
	if len(wire.commands("LOGIN")) != 1 || c.loginRetryAt.Load() != deadline {
		t.Fatal("held requests retried LOGIN or extended the cooldown")
	}
	recovered.Store(true)
	c.loginRetryAt.Store(time.Now().Add(-time.Second).UnixNano())
	if err := c.Connect(); err != nil {
		t.Fatalf("mailbox did not recover after cooldown: %v", err)
	}
	if len(wire.commands("LOGIN")) != 2 {
		t.Fatal("recovery did not authenticate exactly once")
	}
}

func TestIPLoginCooldownProtectsOtherMailboxesOnSameEgress(t *testing.T) {
	c, wire := interceptingServer(t, nil, func(line string) string {
		parts := strings.Fields(line)
		if len(parts) > 1 && strings.EqualFold(parts[1], "LOGIN") {
			return fmt.Sprintf("%s NO [ALERT] EXCESSIVE FAILURES -- IP TEMP BANNED\r\n", parts[0])
		}
		return ""
	})
	if err := c.Connect(); err == nil || err.Code != errx.MailErrorCodeSendingTooFast {
		t.Fatalf("IP ban not recognized: %+v", err)
	}
	key, _ := c.loginEndpoint()
	t.Cleanup(func() {
		loginIPCooldowns.Lock()
		delete(loginIPCooldowns.until, key)
		loginIPCooldowns.Unlock()
	})
	other := clientTo(t, net.JoinHostPort(c.Credentials.Host, fmt.Sprint(c.Credentials.Port)))
	other.Credentials.Username = "another-mailbox@test"
	if err := other.Connect(); err == nil || err.Code != errx.MailErrorCodeSendingTooFast {
		t.Fatalf("another mailbox retried the banned endpoint: %+v", err)
	}
	if len(wire.commands("LOGIN")) != 1 {
		t.Fatal("shared IP ban allowed another login")
	}
	other.BindIP = &net.TCPAddr{IP: net.ParseIP("192.0.2.1")}
	if err := other.loginCooldown(time.Now()); err != nil {
		t.Fatal("IP ban leaked to a different egress")
	}
	other.BindIP = nil
	other.Credentials.Port++
	if err := other.loginCooldown(time.Now()); err != nil {
		t.Fatal("IP ban leaked to a different endpoint")
	}
	other.Credentials.Port--
	if err := other.loginCooldown(time.Now().Add(loginCooldownDuration + time.Second)); err != nil {
		t.Fatal("shared ban did not expire")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	c.Context = ctx
	if err := c.Connect(); err == nil || err.Code != errx.MailErrorCodeServerUnreachable {
		t.Fatal("cancelled mailbox retained a retryable login instead of stopping")
	}
}

func TestAccountLoginCooldownDoesNotHoldOtherMailboxes(t *testing.T) {
	c := clientTo(t, startMemServer(t, nil))
	other := clientTo(t, net.JoinHostPort(c.Credentials.Host, fmt.Sprint(c.Credentials.Port)))
	c.handleError(&goimap.Error{Type: goimap.StatusResponseTypeNo, Text: "LOGIN DENIED -- TOO MANY FAILURES"})
	if err := other.Connect(); err != nil {
		t.Fatalf("mailbox-only lockout spread to another mailbox: %v", err)
	}
	for _, text := range []string{"Invalid credentials", "Application-specific password required"} {
		got := other.handleError(&goimap.Error{Type: goimap.StatusResponseTypeNo, Code: goimap.ResponseCodeAuthenticationFailed, Text: text})
		if got.Code != errx.MailErrorCodeInvalidCredentials || got.RetryAfter != 0 {
			t.Fatalf("genuine credential failure was converted to an automatic retry: %+v", got)
		}
	}
}
