// Package netbind constructs outbound dialers with an optional source IP.
//
// Use cases:
//   - Self-hoster on a single-IP VPS: no env var set, default route is used.
//   - Hosted Warmbly on a multi-IP box: WORKER_BIND_IP set per process so
//     SMTP/IMAP/HTTPS egress leaves from the assigned IP.
//   - Multi-egress per process (future): callers pass an explicit *net.TCPAddr
//     overriding the env-var fallback.
package netbind

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/warmbly/warmbly/internal/config"
	"github.com/warmbly/warmbly/internal/pkg/safehttp"
)

const (
	envBindIP      = "WORKER_BIND_IP"
	envInsecureTLS = "MAIL_TLS_INSECURE"
	defaultTimeout = 10 * time.Second
)

var (
	envOnce sync.Once
	envIP   *net.TCPAddr
)

// FromEnv returns the *net.TCPAddr derived from the WORKER_BIND_IP env var, or
// nil if unset/invalid. The lookup is cached for the lifetime of the process.
func FromEnv() *net.TCPAddr {
	envOnce.Do(func() {
		raw := os.Getenv(envBindIP)
		if raw == "" {
			return
		}
		ip := net.ParseIP(raw)
		if ip == nil {
			return
		}
		envIP = &net.TCPAddr{IP: ip}
	})
	return envIP
}

// Dialer returns a *net.Dialer bound to the given local address. If local is
// nil, the env var is consulted. If still unset, the OS default route is used.
// Unless config.PrivateMailHostsAllowed, it refuses any peer that is not publicly routable.
func Dialer(local *net.TCPAddr) *net.Dialer {
	if local == nil {
		local = FromEnv()
	}
	d := &net.Dialer{
		Timeout:   defaultTimeout,
		LocalAddr: local,
	}
	if !config.PrivateMailHostsAllowed() {
		d.ControlContext = publicPeerOnly
	}
	return d
}

// ErrPrivateMailHost is returned when a mail connection targets a non-public address.
var ErrPrivateMailHost = errors.New("mail server address is not publicly routable")

// publicPeerOnly checks the resolved address each connection attempt is about to use.
func publicPeerOnly(_ context.Context, _, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if safehttp.IsBlockedIP(net.ParseIP(host)) {
		return ErrPrivateMailHost
	}
	return nil
}

// TLSDialer mirrors Dialer for TLS connections (IMAP, HTTPS).
func TLSDialer(local *net.TCPAddr, cfg *tls.Config) *tls.Dialer {
	if cfg != nil && InsecureTLS() {
		cfg.InsecureSkipVerify = true
	}
	return &tls.Dialer{
		NetDialer: Dialer(local),
		Config:    cfg,
	}
}

var (
	insecureOnce sync.Once
	insecureTLS  bool
)

// InsecureTLS reports whether MAIL_TLS_INSECURE=true applies. A self-hosted or
// development instance honours it; hosted production ignores it, so a server
// without a valid certificate needs the mailbox's own pinned opt-out there.
func InsecureTLS() bool {
	insecureOnce.Do(func() {
		set := os.Getenv(envInsecureTLS) == "true"
		insecureTLS = config.MailTLSInsecureAllowed()
		switch {
		case insecureTLS:
			log.Warn().Msg("MAIL_TLS_INSECURE=true: mailbox certificates are not verified and SMTP may skip STARTTLS. Unset it to verify them; a mailbox on a server with its own certificate can trust that server in its own settings instead")
		case set:
			log.Error().Msg("MAIL_TLS_INSECURE=true is ignored on hosted production: mailbox certificates are verified, and a mailbox on a server with its own certificate trusts it in its own settings. Remove the variable from this node's environment")
		}
	})
	return insecureTLS
}

// LoopbackPeer reports whether conn's peer is this machine.
//
// The host string was already checked before dialing, but a name check is a
// promise about DNS, not about the socket: the answer can change between the
// two. This asks the connection itself, which is the only thing that cannot
// be moved after the fact, and is what makes the cleartext mailbox mode
// (models.MailSecurityNone) safe to obey.
func LoopbackPeer(conn net.Conn) bool {
	if conn == nil {
		return false
	}
	addr, ok := conn.RemoteAddr().(*net.TCPAddr)
	if !ok {
		return false
	}
	return addr.IP.IsLoopback()
}
