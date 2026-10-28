package email

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/warmbly/warmbly/internal/client/netbind"
	"github.com/warmbly/warmbly/internal/models"
)

// TestProbeFail_ReplyOutranksDeadline: a server reply that lands as the
// deadline passes is still that reply, so a refused password is never
// reported as a timeout the person would retry.
func TestProbeFail_ReplyOutranksDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()

	refused := &textproto.Error{Code: 535, Msg: "5.7.8 Username and Password not accepted"}
	if res := probeFail(ctx, models.MailProbeAuthRefused, refused); res.Reason != models.MailProbeAuthRefused {
		t.Fatalf("got %+v, want auth_refused", res)
	}
	// Without a reply, the deadline is what happened.
	if res := probeFail(ctx, models.MailProbeProtocol, errors.New("EOF")); res.Reason != models.MailProbeTimeout {
		t.Fatalf("got %+v, want timeout", res)
	}
}

// TestProbeFail_SocketErrorsAreClosedWords: a net.OpError carries the
// worker's own bound address, which never reaches the customer.
func TestProbeFail_SocketErrorsAreClosedWords(t *testing.T) {
	op := &net.OpError{Op: "write", Net: "tcp",
		Source: &net.TCPAddr{IP: net.IPv4(10, 0, 0, 5), Port: 51234},
		Addr:   &net.TCPAddr{IP: net.IPv4(203, 0, 113, 9), Port: 587},
		Err:    errors.New("write: broken pipe")}
	res := probeFail(context.Background(), models.MailProbeTLS, op)
	if res.Reason != models.MailProbeTLS || res.Detail != "the connection failed" {
		t.Fatalf("got %+v", res)
	}
	dns := &net.DNSError{Err: "no such host", Name: "x.invalid", IsNotFound: true}
	if res := probeFail(context.Background(), models.MailProbeUnreachable, &net.OpError{Op: "dial", Err: dns}); res.Detail != "the host name does not exist" {
		t.Fatalf("got %+v", res)
	}
}

// A refused private address is reported as that, not as a failed connection.
func TestProbeFail_PrivateHostIsNamed(t *testing.T) {
	err := &net.OpError{Op: "dial", Net: "tcp", Err: fmt.Errorf("dial 10.0.0.5:587: %w", netbind.ErrPrivateMailHost)}
	res := probeFail(context.Background(), models.MailProbeUnreachable, err)
	if res.Reason != models.MailProbeUnreachable || res.Detail != models.MailProbeDetailPrivateHost {
		t.Fatalf("got %+v", res)
	}
}

// Each certificate refusal says why in plain words.
func TestCertificateDetail(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{x509.HostnameError{Certificate: &x509.Certificate{}, Host: "mail.example.org"}, "different host name"},
		{x509.CertificateInvalidError{Reason: x509.Expired}, "expired"},
		{x509.UnknownAuthorityError{}, "self-signed or company certificate"},
		{&tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}, "self-signed or company certificate"},
	} {
		if got := certificateDetail(tc.err); !strings.Contains(got, tc.want) {
			t.Errorf("certificateDetail(%T) = %q, want %q", tc.err, got, tc.want)
		}
	}
	if got := certificateDetail(errors.New("tls: first record does not look like a TLS handshake")); got != "" {
		t.Errorf("non-certificate error described as a certificate: %q", got)
	}
}
