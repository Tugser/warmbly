// Package mailtls builds the TLS configuration for a mailbox's SMTP and IMAP
// connections.
//
// By default a server's certificate is verified against the system roots and
// the host name. A mailbox whose owner explicitly opted out of that check (a
// self-signed or private-CA server) is trusted on its pinned certificate
// instead: the SHA-256 of the leaf's SubjectPublicKeyInfo, recorded when the
// owner confirmed it, and compared on every connection.
package mailtls

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/warmbly/warmbly/internal/client/netbind"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

// Policy is how one server's certificate is trusted.
type Policy struct {
	// Unverified is the owner's recorded choice to skip CA verification.
	Unverified bool
	// Pin is the confirmed SPKI SHA-256 fingerprint, lowercase hex.
	Pin string
	// Record accepts an unverified server with no pin so a connection test
	// can report its fingerprint for the owner to confirm. Probes only.
	Record bool
}

// PolicyFor reads the policy a mailbox's stored settings describe.
func PolicyFor(svc *models.Service) Policy {
	if svc == nil {
		return Policy{}
	}
	return Policy{Unverified: svc.TLSUnverified, Pin: NormalizePin(svc.TLSPin)}
}

// Mismatch is why a pinned server was refused.
type Mismatch int

const (
	// PinChanged is a certificate other than the one confirmed.
	PinChanged Mismatch = iota + 1
	// PinMissing is an opted-out server with no confirmed certificate.
	PinMissing
)

// PinError is a handshake refused by the pin check.
type PinError struct {
	Kind     Mismatch
	Observed string
}

func (e *PinError) Error() string {
	if e.Kind == PinMissing {
		return "mail server certificate has not been confirmed for this mailbox"
	}
	return "mail server certificate does not match the one confirmed for this mailbox"
}

// Capture holds the leaf certificate the last handshake presented.
type Capture struct {
	mu      sync.Mutex
	leaf    *x509.Certificate
	trusted bool
	refusal *PinError
}

func (c *Capture) record(leaf *x509.Certificate, trusted bool, refusal *PinError) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.leaf, c.trusted, c.refusal = leaf, trusted, refusal
	c.mu.Unlock()
}

// Refusal is the pin check's refusal of the last handshake, if any.
func (c *Capture) Refusal() *PinError {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.refusal
}

// Certificate describes the leaf the server presented, from the capture or
// from a CA verification failure carried by err.
func (c *Capture) Certificate(err error) *models.MailCertificate {
	var leaf *x509.Certificate
	trusted := false
	if c != nil {
		c.mu.Lock()
		leaf, trusted = c.leaf, c.trusted
		c.mu.Unlock()
	}
	if leaf == nil {
		var verr *tls.CertificateVerificationError
		if errors.As(err, &verr) && len(verr.UnverifiedCertificates) > 0 {
			leaf = verr.UnverifiedCertificates[0]
		}
	}
	if leaf == nil {
		return nil
	}
	desc := Describe(leaf)
	desc.Trusted = trusted
	return desc
}

// Describe summarises a certificate for the owner to confirm.
func Describe(cert *x509.Certificate) *models.MailCertificate {
	return &models.MailCertificate{
		Fingerprint: Fingerprint(cert),
		Subject:     cert.Subject.String(),
		Issuer:      cert.Issuer.String(),
		NotAfter:    cert.NotAfter.UTC().Format(time.RFC3339),
		SelfSigned:  cert.Subject.String() == cert.Issuer.String(),
	}
}

// Fingerprint is the lowercase hex SHA-256 of the certificate's SPKI.
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return hex.EncodeToString(sum[:])
}

// NormalizePin accepts a fingerprint with or without colons, spaces or a
// "sha256:" prefix, in either case, and returns it as 64 lowercase hex
// characters, or "" when it is not one.
func NormalizePin(pin string) string {
	return models.NormalizeTLSPin(pin)
}

// Config returns the TLS configuration for host under policy p, recording
// the presented certificate into capture (which may be nil).
func Config(host string, p Policy, capture *Capture) *tls.Config {
	cfg := &tls.Config{
		ServerName: host,
		MinVersion: tls.VersionTLS12,
	}
	if !p.Unverified {
		skip := netbind.InsecureTLS()
		cfg.InsecureSkipVerify = skip //nolint:gosec // MAIL_TLS_INSECURE, honoured only in development
		cfg.VerifyConnection = func(cs tls.ConnectionState) error {
			capture.record(leafOf(cs), !skip, nil)
			return nil
		}
		return cfg
	}
	// Verification is replaced by the pin check, not dropped: the handshake
	// fails unless the leaf matches the confirmed fingerprint.
	cfg.InsecureSkipVerify = true //nolint:gosec // the certificate is checked against the mailbox's pin in VerifyConnection
	pin := NormalizePin(p.Pin)
	cfg.VerifyConnection = func(cs tls.ConnectionState) error {
		leaf := leafOf(cs)
		refusal := verifyPin(leaf, pin, p.Record)
		capture.record(leaf, caTrusted(host, cs), refusal)
		if refusal != nil {
			return refusal
		}
		return nil
	}
	return cfg
}

func verifyPin(leaf *x509.Certificate, pin string, record bool) *PinError {
	if leaf == nil {
		return &PinError{Kind: PinMissing}
	}
	got := Fingerprint(leaf)
	switch {
	case pin == "" && record:
		return nil
	case pin == "":
		return &PinError{Kind: PinMissing, Observed: got}
	case got != pin:
		return &PinError{Kind: PinChanged, Observed: got}
	}
	return nil
}

// caTrusted reports whether the chain would also pass CA and host name verification.
func caTrusted(host string, cs tls.ConnectionState) bool {
	if len(cs.PeerCertificates) == 0 {
		return false
	}
	inter := x509.NewCertPool()
	for _, c := range cs.PeerCertificates[1:] {
		inter.AddCert(c)
	}
	_, err := cs.PeerCertificates[0].Verify(x509.VerifyOptions{DNSName: host, Intermediates: inter})
	return err == nil
}

func leafOf(cs tls.ConnectionState) *x509.Certificate {
	if len(cs.PeerCertificates) == 0 {
		return nil
	}
	return cs.PeerCertificates[0]
}

// MailError is the mailbox error for a pin refusal on the named server
// ("SMTP" or "IMAP").
func MailError(perr *PinError, server string) *errx.MailError {
	if perr.Kind == PinMissing {
		return errx.ErrMailTLSCertificateUnconfirmed(server)
	}
	return errx.ErrMailTLSCertificateChanged(server)
}

// AsPinError finds a pin refusal in err or the capture.
func AsPinError(err error, capture *Capture) *PinError {
	var perr *PinError
	if errors.As(err, &perr) {
		return perr
	}
	if err != nil {
		return capture.Refusal()
	}
	return nil
}

// CertificateRefused reports whether err is a certificate that failed CA, host name or validity checks.
func CertificateRefused(err error) bool {
	var verr *tls.CertificateVerificationError
	var unknown x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	var host x509.HostnameError
	return errors.As(err, &verr) || errors.As(err, &unknown) || errors.As(err, &invalid) || errors.As(err, &host)
}
