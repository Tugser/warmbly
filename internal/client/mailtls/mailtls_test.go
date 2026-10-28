package mailtls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

func selfSigned(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "mail.internal.test"},
		DNSNames:     []string{"mail.internal.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
}

// handshake runs one TLS handshake against a server presenting cert.
func handshake(t *testing.T, cert tls.Certificate, p Policy) (*Capture, error) {
	t.Helper()
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	go func() {
		s := tls.Server(server, &tls.Config{Certificates: []tls.Certificate{cert}})
		_ = s.Handshake()
	}()
	capture := &Capture{}
	conn := tls.Client(client, Config("mail.internal.test", p, capture))
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	return capture, conn.Handshake()
}

func TestPinnedCertificateMatches(t *testing.T) {
	cert := selfSigned(t)
	pin := Fingerprint(cert.Leaf)
	capture, err := handshake(t, cert, Policy{Unverified: true, Pin: strings.ToUpper(pin)})
	if err != nil {
		t.Fatalf("handshake with the confirmed pin: %v", err)
	}
	if got := capture.Certificate(nil); got == nil || got.Fingerprint != pin {
		t.Fatalf("captured %+v, want fingerprint %s", got, pin)
	}
}

func TestPinnedCertificateMismatchIsRefused(t *testing.T) {
	cert := selfSigned(t)
	other := Fingerprint(selfSigned(t).Leaf)
	capture, err := handshake(t, cert, Policy{Unverified: true, Pin: other})
	perr := AsPinError(err, capture)
	if perr == nil || perr.Kind != PinChanged {
		t.Fatalf("err = %v, want a changed-certificate refusal", err)
	}
	if perr.Observed != Fingerprint(cert.Leaf) {
		t.Fatalf("observed %s, want the presented fingerprint", perr.Observed)
	}
	if code := MailError(perr, "SMTP").Code; code != errx.MailErrorCodeTLSCertificateChanged {
		t.Fatalf("code = %s", code)
	}
}

func TestOptedOutWithoutPinIsRefused(t *testing.T) {
	cert := selfSigned(t)
	capture, err := handshake(t, cert, Policy{Unverified: true})
	perr := AsPinError(err, capture)
	if perr == nil || perr.Kind != PinMissing {
		t.Fatalf("err = %v, want an unconfirmed-certificate refusal", err)
	}
	if code := MailError(perr, "IMAP").Code; code != errx.MailErrorCodeTLSCertificateUnconfirmed {
		t.Fatalf("code = %s", code)
	}
	if capture.Certificate(err) == nil {
		t.Fatal("the presented certificate was not captured for the owner to confirm")
	}
}

func TestRecordModeAcceptsAndReportsTheCertificate(t *testing.T) {
	cert := selfSigned(t)
	capture, err := handshake(t, cert, Policy{Unverified: true, Record: true})
	if err != nil {
		t.Fatalf("record-mode handshake: %v", err)
	}
	got := capture.Certificate(nil)
	if got == nil || got.Fingerprint != Fingerprint(cert.Leaf) || !got.SelfSigned || got.Trusted {
		t.Fatalf("captured %+v", got)
	}
}

func TestVerificationOnRejectsAnUntrustedCertificate(t *testing.T) {
	cert := selfSigned(t)
	// A pin is ignored while verification is on.
	capture, err := handshake(t, cert, Policy{Pin: Fingerprint(cert.Leaf)})
	if err == nil {
		t.Fatal("a self-signed certificate passed CA verification")
	}
	var verr *tls.CertificateVerificationError
	if !errors.As(err, &verr) {
		t.Fatalf("err = %v, want a certificate verification error", err)
	}
	if AsPinError(err, capture) != nil {
		t.Fatal("a CA failure was reported as a pin refusal")
	}
	if got := capture.Certificate(err); got == nil || got.Fingerprint != Fingerprint(cert.Leaf) {
		t.Fatalf("the rejected certificate was not described: %+v", got)
	}
}

func TestNormalizePin(t *testing.T) {
	want := strings.Repeat("ab", 32)
	for _, in := range []string{want, strings.ToUpper(want), "sha256:" + want, "AB:" + strings.Repeat("ab:", 30) + "AB"} {
		if got := NormalizePin(in); got != want {
			t.Errorf("NormalizePin(%q) = %q", in, got)
		}
	}
	for _, in := range []string{"", "abc", strings.Repeat("zz", 32)} {
		if got := NormalizePin(in); got != "" {
			t.Errorf("NormalizePin(%q) = %q, want empty", in, got)
		}
	}
}

func TestPolicyForReadsTheStoredSettings(t *testing.T) {
	pin := strings.Repeat("0f", 32)
	p := PolicyFor(&models.Service{TLSUnverified: true, TLSPin: strings.ToUpper(pin)})
	if !p.Unverified || p.Pin != pin || p.Record {
		t.Fatalf("policy = %+v", p)
	}
	if p := PolicyFor(nil); p.Unverified {
		t.Fatalf("nil service policy = %+v", p)
	}
}
