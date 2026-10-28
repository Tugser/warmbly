package netbind

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"testing"
	"time"
)

func TestDialer_ExplicitBindIP(t *testing.T) {
	want := &net.TCPAddr{IP: net.ParseIP("127.0.0.1")}
	d := Dialer(want)
	if d.LocalAddr != want {
		t.Fatalf("LocalAddr = %v, want %v", d.LocalAddr, want)
	}
	if d.Timeout != defaultTimeout {
		t.Fatalf("Timeout = %v, want %v", d.Timeout, defaultTimeout)
	}
}

func TestDialer_NilFallsBackToEnv(t *testing.T) {
	// FromEnv is cached via sync.Once so we can't manipulate the env mid-test;
	// just verify the call doesn't panic and returns a usable dialer.
	d := Dialer(nil)
	if d == nil {
		t.Fatal("Dialer(nil) returned nil")
	}
	if d.Timeout == 0 {
		t.Fatal("dialer should have a default timeout")
	}
}

func TestTLSDialer_WrapsNetDialer(t *testing.T) {
	bindIP := &net.TCPAddr{IP: net.ParseIP("127.0.0.1")}
	cfg := &tls.Config{ServerName: "example.com"}
	td := TLSDialer(bindIP, cfg)
	if td.Config != cfg {
		t.Fatal("tls.Dialer.Config not propagated")
	}
	if td.NetDialer == nil {
		t.Fatal("tls.Dialer.NetDialer is nil")
	}
	if td.NetDialer.LocalAddr != bindIP {
		t.Fatal("tls.Dialer.NetDialer.LocalAddr not propagated")
	}
}

func TestDefaultTimeoutIsReasonable(t *testing.T) {
	if defaultTimeout < time.Second || defaultTimeout > time.Minute {
		t.Fatalf("defaultTimeout = %v looks wrong", defaultTimeout)
	}
}

// A loopback listener stands in for a private mail server.
func TestDialer_RefusesPrivatePeerOnHostedProduction(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	t.Setenv("DEPLOYMENT_MODE", "cloud")
	t.Setenv("APP_ENV", "prod")
	t.Setenv("MAIL_ALLOW_PRIVATE_HOSTS", "true")
	if _, err := Dialer(nil).Dial("tcp", ln.Addr().String()); !errors.Is(err, ErrPrivateMailHost) {
		t.Fatalf("hosted dial err = %v, want ErrPrivateMailHost", err)
	}

	t.Setenv("DEPLOYMENT_MODE", "self_hosted")
	t.Setenv("MAIL_ALLOW_PRIVATE_HOSTS", "")
	conn, err := Dialer(nil).Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("self-host dial err = %v", err)
	}
	_ = conn.Close()

	t.Setenv("MAIL_ALLOW_PRIVATE_HOSTS", "false")
	if _, err := Dialer(nil).Dial("tcp", ln.Addr().String()); !errors.Is(err, ErrPrivateMailHost) {
		t.Fatalf("self-host opted out dial err = %v, want ErrPrivateMailHost", err)
	}
}

func TestPublicPeerOnly(t *testing.T) {
	for addr, blocked := range map[string]bool{
		"127.0.0.1:993":       true,
		"10.0.0.5:587":        true,
		"169.254.169.254:143": true,
		"100.64.1.1:25":       true,
		"[::1]:993":           true,
		"[fd00::1]:993":       true,
		"224.0.0.1:993":       true,
		"0.0.0.0:993":         true,
		"8.8.8.8:993":         false,
		"[2606:4700::1]:465":  false,
	} {
		err := publicPeerOnly(context.Background(), "tcp", addr, nil)
		if got := err != nil; got != blocked {
			t.Errorf("%s: blocked = %v, want %v", addr, got, blocked)
		}
	}
}
