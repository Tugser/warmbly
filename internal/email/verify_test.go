package email

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/warmbly/warmbly/internal/client/mailtls"
	"github.com/warmbly/warmbly/internal/models"
)

// TestVerifySMTP_UnreachableHost guards against a nil-conn deref. A wrong SMTP
// host is ordinary onboarding input, and these probes run in bare goroutines
// inside the worker, so a panic here takes the worker down along with every
// mailbox assigned to it.
func TestVerifySMTP_UnreachableHost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	for _, tc := range []struct {
		name     string
		host     string
		port     int
		security string
	}{
		// Each security mode takes a different dial path (implicit TLS vs
		// plaintext + STARTTLS), and an empty mode falls back to the port.
		{"closed port, 587", "127.0.0.1", 587, ""},
		{"closed port, 465", "127.0.0.1", 465, ""},
		{"unresolvable host", "no-such-mail-host.invalid", 587, ""},
		{"unresolvable host, implicit tls", "no-such-mail-host.invalid", 465, ""},
		// Non-standard ports are accepted now, so the mode carries the choice.
		{"closed nonstandard port, starttls", "127.0.0.1", 2525, models.MailSecurityStartTLS},
		{"closed nonstandard port, implicit tls", "127.0.0.1", 8465, models.MailSecurityTLS},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("VerifySMTP panicked: %v", r)
				}
			}()
			res := VerifySMTP(ctx, tc.host, tc.port, "user", "pass", tc.security, mailtls.Policy{})
			if res.OK {
				t.Fatal("VerifySMTP returned true for an unreachable server")
			}
			if res.Reason != models.MailProbeUnreachable {
				t.Fatalf("reason = %q (%s), want unreachable", res.Reason, res.Detail)
			}
			// The detail is a closed sentence: a raw dial error names the
			// worker's own bound address alongside the peer.
			if strings.Contains(res.Detail, "dial tcp") || strings.Contains(res.Detail, "127.0.0.1") {
				t.Fatalf("detail %q leaks the dial error", res.Detail)
			}
		})
	}
}

// TestVerifyImap_UnreachableHost is the same guard for the IMAP probe, across
// both security modes.
func TestVerifyImap_UnreachableHost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	for _, tc := range []struct {
		name     string
		host     string
		port     int
		security string
	}{
		{"unresolvable host, implicit tls", "no-such-mail-host.invalid", 993, ""},
		{"unresolvable host, starttls", "no-such-mail-host.invalid", 143, ""},
		{"closed port, starttls", "127.0.0.1", 143, models.MailSecurityStartTLS},
		{"closed nonstandard port, implicit tls", "127.0.0.1", 9993, models.MailSecurityTLS},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("VerifyImap panicked: %v", r)
				}
			}()
			res := VerifyImap(ctx, tc.host, tc.port, "user", "pass", tc.security, mailtls.Policy{})
			if res.OK {
				t.Fatal("VerifyImap returned true for an unreachable server")
			}
			if res.Reason != models.MailProbeUnreachable {
				t.Fatalf("reason = %q (%s), want unreachable", res.Reason, res.Detail)
			}
		})
	}
}

// fakeServer runs handle for every connection on a loopback port. The probes
// reach it in the "none" mode, which is legal for a loopback literal on a
// self-hosted instance (the default in tests).
func fakeServer(t *testing.T, handle func(conn net.Conn)) (string, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				handle(conn)
			}()
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", addr.Port
}

// smtpRefusing is a server that greets, advertises AUTH, and answers every
// AUTH with reply.
func smtpRefusing(reply string) func(net.Conn) {
	return func(conn net.Conn) {
		r := bufio.NewReader(conn)
		_, _ = conn.Write([]byte("220 fake ESMTP\r\n"))
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			switch cmd := strings.ToUpper(strings.Fields(line)[0]); cmd {
			case "EHLO", "HELO":
				_, _ = conn.Write([]byte("250-fake\r\n250 AUTH PLAIN LOGIN\r\n"))
			case "AUTH":
				_, _ = conn.Write([]byte(reply + "\r\n"))
			case "QUIT":
				_, _ = conn.Write([]byte("221 bye\r\n"))
				return
			default:
				_, _ = conn.Write([]byte("500 what\r\n"))
			}
		}
	}
}

// TestVerifySMTP_Classifies checks that the server's answer decides the
// reason: a 5xx on AUTH is a refused sign-in, a 4xx a temporary one, and a
// server that never speaks is a timeout. Before this, all three were "invalid
// credentials".
func TestVerifySMTP_Classifies(t *testing.T) {
	t.Run("5xx is auth_refused with the reply", func(t *testing.T) {
		host, port := fakeServer(t, smtpRefusing("535 5.7.8 Username and Password not accepted"))
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		res := VerifySMTP(ctx, host, port, "user", "pass", models.MailSecurityNone, mailtls.Policy{})
		if res.OK || res.Reason != models.MailProbeAuthRefused {
			t.Fatalf("got %+v, want auth_refused", res)
		}
		if !strings.Contains(res.Detail, "5.7.8") {
			t.Fatalf("detail %q should carry the server's reply", res.Detail)
		}
	})
	t.Run("534 is auth_refused too", func(t *testing.T) {
		host, port := fakeServer(t, smtpRefusing("534 5.7.9 Application-specific password required"))
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		res := VerifySMTP(ctx, host, port, "user", "pass", models.MailSecurityNone, mailtls.Policy{})
		if res.OK || res.Reason != models.MailProbeAuthRefused {
			t.Fatalf("got %+v, want auth_refused", res)
		}
	})
	t.Run("other 5xx is the conversation, not the password", func(t *testing.T) {
		host, port := fakeServer(t, smtpRefusing("504 5.7.4 Unrecognized authentication type"))
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		res := VerifySMTP(ctx, host, port, "user", "pass", models.MailSecurityNone, mailtls.Policy{})
		if res.OK || res.Reason != models.MailProbeProtocol {
			t.Fatalf("got %+v, want protocol", res)
		}
	})
	t.Run("a server that hangs up leaks no socket address", func(t *testing.T) {
		host, port := fakeServer(t, func(conn net.Conn) {
			r := bufio.NewReader(conn)
			_, _ = conn.Write([]byte("220 fake ESMTP\r\n"))
			_, _ = r.ReadString('\n') // EHLO, then hang up
		})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		res := VerifySMTP(ctx, host, port, "user", "pass", models.MailSecurityNone, mailtls.Policy{})
		if res.OK || res.Reason == models.MailProbeAuthRefused {
			t.Fatalf("got %+v", res)
		}
		if strings.Contains(res.Detail, "127.0.0.1") || strings.Contains(res.Detail, "->") {
			t.Fatalf("detail %q leaks a socket address", res.Detail)
		}
	})
	t.Run("4xx is temporary", func(t *testing.T) {
		host, port := fakeServer(t, smtpRefusing("454 4.7.0 Too many login attempts, please try again later"))
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		res := VerifySMTP(ctx, host, port, "user", "pass", models.MailSecurityNone, mailtls.Policy{})
		if res.OK || res.Reason != models.MailProbeTemporary {
			t.Fatalf("got %+v, want temporary", res)
		}
	})
	t.Run("silent server is a timeout, not a refusal", func(t *testing.T) {
		host, port := fakeServer(t, func(conn net.Conn) { time.Sleep(3 * time.Second) })
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		start := time.Now()
		res := VerifySMTP(ctx, host, port, "user", "pass", models.MailSecurityNone, mailtls.Policy{})
		if res.OK || res.Reason != models.MailProbeTimeout {
			t.Fatalf("got %+v, want timeout", res)
		}
		if time.Since(start) > 2*time.Second {
			t.Fatal("the probe outlived its context")
		}
	})
}

// imapRefusing is a server that greets and answers every LOGIN with reply.
func imapRefusing(reply string) func(net.Conn) {
	return func(conn net.Conn) {
		r := bufio.NewReader(conn)
		_, _ = conn.Write([]byte("* OK fake ready\r\n"))
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			f := strings.Fields(line)
			if len(f) < 2 {
				continue
			}
			tag := f[0]
			switch strings.ToUpper(f[1]) {
			case "CAPABILITY":
				_, _ = conn.Write([]byte("* CAPABILITY IMAP4rev1\r\n" + tag + " OK done\r\n"))
			case "LOGIN":
				_, _ = conn.Write([]byte(tag + " " + reply + "\r\n"))
			case "LOGOUT":
				_, _ = conn.Write([]byte("* BYE\r\n" + tag + " OK\r\n"))
				return
			default:
				_, _ = conn.Write([]byte(tag + " BAD what\r\n"))
			}
		}
	}
}

// TestVerifyImap_Classifies is the IMAP half of TestVerifySMTP_Classifies.
func TestVerifyImap_Classifies(t *testing.T) {
	t.Run("NO on LOGIN is auth_refused with the reply", func(t *testing.T) {
		host, port := fakeServer(t, imapRefusing("NO [AUTHENTICATIONFAILED] Invalid credentials (Failure)"))
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		res := VerifyImap(ctx, host, port, "user", "pass", models.MailSecurityNone, mailtls.Policy{})
		if res.OK || res.Reason != models.MailProbeAuthRefused {
			t.Fatalf("got %+v, want auth_refused", res)
		}
		if !strings.Contains(res.Detail, "Invalid credentials") {
			t.Fatalf("detail %q should carry the server's reply", res.Detail)
		}
	})
	t.Run("BAD is the conversation, not the password", func(t *testing.T) {
		host, port := fakeServer(t, imapRefusing("BAD Invalid command"))
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		res := VerifyImap(ctx, host, port, "user", "pass", models.MailSecurityNone, mailtls.Policy{})
		if res.OK || res.Reason != models.MailProbeProtocol {
			t.Fatalf("got %+v, want protocol", res)
		}
	})
	t.Run("refusal is classified before logout, even if logout is never answered", func(t *testing.T) {
		host, port := fakeServer(t, func(conn net.Conn) {
			r := bufio.NewReader(conn)
			_, _ = conn.Write([]byte("* OK fake ready\r\n"))
			for {
				line, err := r.ReadString('\n')
				if err != nil {
					return
				}
				f := strings.Fields(line)
				if len(f) >= 2 && strings.EqualFold(f[1], "LOGIN") {
					_, _ = conn.Write([]byte(f[0] + " NO [AUTHENTICATIONFAILED] Invalid credentials (Failure)\r\n"))
				}
				// Everything else, LOGOUT included, is left unanswered.
			}
		})
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		defer cancel()
		start := time.Now()
		res := VerifyImap(ctx, host, port, "user", "pass", models.MailSecurityNone, mailtls.Policy{})
		if res.OK || res.Reason != models.MailProbeAuthRefused {
			t.Fatalf("got %+v, want auth_refused", res)
		}
		if time.Since(start) > time.Second {
			t.Fatal("the probe waited for a LOGOUT answer")
		}
	})
	t.Run("UNAVAILABLE is temporary", func(t *testing.T) {
		host, port := fakeServer(t, imapRefusing("NO [UNAVAILABLE] Temporary System Error"))
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		res := VerifyImap(ctx, host, port, "user", "pass", models.MailSecurityNone, mailtls.Policy{})
		if res.OK || res.Reason != models.MailProbeTemporary {
			t.Fatalf("got %+v, want temporary", res)
		}
	})
	t.Run("silent server is a timeout, not a refusal", func(t *testing.T) {
		host, port := fakeServer(t, func(conn net.Conn) { time.Sleep(3 * time.Second) })
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		start := time.Now()
		res := VerifyImap(ctx, host, port, "user", "pass", models.MailSecurityNone, mailtls.Policy{})
		if res.OK || res.Reason != models.MailProbeTimeout {
			t.Fatalf("got %+v, want timeout", res)
		}
		if time.Since(start) > 2*time.Second {
			t.Fatal("the probe outlived its context")
		}
	})
}
