package smtp

import (
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/warmbly/warmbly/internal/models"
)

// Every retryable SMTP stage keeps the stable code and its diagnostic detail.
func TestUnreachableNamesTheStepThatFailed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		at     string
		reply  string
		stage  string
		detail string
	}{
		{"refused auth", "AUTH", "454 4.7.0 temporary authentication failure", "auth", "454"},
		{"deferred sender", "MAIL", "451 4.7.1 try again later", "mail from", "451"},
		{"greylisted recipient", "RCPT", "450 4.2.0 greylisted", "rcpt to", "450"},
		{"deferred message", "DATA-END", "451 4.3.0 try later", "message accept", "451"},
	} {
		srv := newRejectingServer(t, "LOGIN", map[string]string{tc.at: tc.reply})
		host, port := srv.addr()
		c := newTestClient(host, port)

		err := c.sendRaw(t.Context(), "sender@warmbly.test", []string{"to@example.test"}, []byte("Subject: hi\r\n\r\nbody\r\n"))
		if err == nil {
			t.Errorf("%s: send succeeded against %q", tc.name, tc.reply)
			continue
		}
		if string(err.Code) != "SERVER_UNREACHABLE" {
			t.Errorf("%s: code = %q, want SERVER_UNREACHABLE", tc.name, err.Code)
		}
		if !strings.Contains(err.Message, tc.stage) {
			t.Errorf("%s: message %q does not name the step %q", tc.name, err.Message, tc.stage)
		}
		if !strings.Contains(err.Message, tc.detail) {
			t.Errorf("%s: message %q dropped the server's own reply %q", tc.name, err.Message, tc.detail)
		}
	}
}

// A refused connection identifies the dial and remote port.
func TestUnreachableNamesARefusedDial(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port := 0
	for _, r := range portStr {
		port = port*10 + int(r-'0')
	}
	// Closed before anything dials it, so the connect is refused rather than queued.
	_ = ln.Close()

	c := newTestClient(host, port)
	mailErr := c.sendRaw(t.Context(), "sender@warmbly.test", []string{"to@example.test"}, []byte("Subject: hi\r\n\r\nbody\r\n"))
	if mailErr == nil {
		t.Fatal("send succeeded against a closed port")
	}
	if string(mailErr.Code) != "SERVER_UNREACHABLE" {
		t.Fatalf("code = %q, want SERVER_UNREACHABLE", mailErr.Code)
	}
	for _, want := range []string{"dial", portStr, "refused"} {
		if !strings.Contains(mailErr.Message, want) {
			t.Errorf("message %q does not mention %q", mailErr.Message, want)
		}
	}
}

// Missing STARTTLS is distinguished from an unreachable server.
func TestUnreachableSaysWhenTheServerOffersNoSTARTTLS(t *testing.T) {
	srv := newFakeServer(t, "LOGIN")
	host, port := srv.addr()
	c := newTestClient(host, port)
	c.plaintext = false

	err := c.sendRaw(t.Context(), "sender@warmbly.test", []string{"to@example.test"}, []byte("Subject: hi\r\n\r\nbody\r\n"))
	if err == nil {
		t.Fatal("sent over a connection that was never encrypted")
	}
	if string(err.Code) != "SERVER_UNREACHABLE" {
		t.Fatalf("code = %q, want SERVER_UNREACHABLE", err.Code)
	}
	if !strings.Contains(err.Message, "starttls") || !strings.Contains(err.Message, "offers no STARTTLS on this port") {
		t.Errorf("message %q does not explain the missing STARTTLS", err.Message)
	}
}

// Implicit TLS reports its handshake failure at the dial stage.
func TestUnreachableNamesTheDialForImplicitTLS(t *testing.T) {
	srv := newFakeServer(t, "LOGIN")
	host, port := srv.addr()
	c := newTestClient(host, port)
	c.Credentials.Security = models.MailSecurityTLS

	err := c.sendRaw(t.Context(), "sender@warmbly.test", []string{"to@example.test"}, []byte("Subject: hi\r\n\r\nbody\r\n"))
	if err == nil {
		t.Fatal("a TLS handshake succeeded against a plaintext server")
	}
	if !strings.Contains(err.Message, "dial") {
		t.Errorf("message %q does not name the dial, where implicit TLS fails", err.Message)
	}
}

// dialCause preserves the failure without exposing network addresses.
func TestDialCauseKeepsTheFailureAndDropsTheAddresses(t *testing.T) {
	op := &net.OpError{
		Op:     "dial",
		Net:    "tcp",
		Source: &net.TCPAddr{IP: net.ParseIP("10.1.2.3")},
		Addr:   &net.TCPAddr{IP: net.ParseIP("203.0.113.9"), Port: 465},
		Err:    errors.New("bind: cannot assign requested address"),
	}
	got := dialCause(op).Error()
	for _, unwanted := range []string{"10.1.2.3", "203.0.113.9"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("%q still carries the address %s", got, unwanted)
		}
	}
	if !strings.Contains(got, "cannot assign requested address") {
		t.Errorf("%q dropped the failure itself", got)
	}
}

func TestDialCausePassesThroughAnythingElse(t *testing.T) {
	plain := errors.New("no such host")
	if got := dialCause(plain); got != plain {
		t.Fatalf("dialCause rewrote a non-OpError: %v", got)
	}
}

// A private address is named as such, with what to use instead.
func TestUnreachableSaysWhenTheHostIsPrivate(t *testing.T) {
	t.Setenv("DEPLOYMENT_MODE", "cloud")
	t.Setenv("APP_ENV", "prod")
	srv := newFakeServer(t, "LOGIN")
	host, port := srv.addr()
	c := newTestClient(host, port)

	mailErr := c.sendRaw(t.Context(), "sender@warmbly.test", []string{"to@example.test"}, []byte("Subject: hi\r\n\r\nbody\r\n"))
	if mailErr == nil {
		t.Fatal("send succeeded against a loopback server on a hosted instance")
	}
	if string(mailErr.Code) != "SERVER_UNREACHABLE" || !strings.Contains(mailErr.Message, "private network address") {
		t.Fatalf("got %s %q", mailErr.Code, mailErr.Message)
	}
	if info := mailErr.GetUserErrorInfo(); info.Title != "Server address is not public" {
		t.Fatalf("title = %q", info.Title)
	}
}
