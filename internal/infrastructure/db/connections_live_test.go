package db

import (
	"context"
	"net/url"
	"os"
	"testing"
)

func TestLiveTransactionPoolingAndDirectLocks(t *testing.T) {
	primary, direct := os.Getenv("WARMBLY_TEST_POOLED_DB"), os.Getenv("WARMBLY_TEST_DIRECT_DB")
	if primary == "" || direct == "" {
		t.Skip("local pooling fixture not configured")
	}
	for _, endpoint := range []string{primary, direct} {
		u, err := url.Parse(endpoint)
		if err != nil || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") || u.Path != "/pooling_fixture" {
			t.Fatal("requires the isolated local pooling_fixture database")
		}
	}
	t.Setenv("DB_POOL_MODE", "transaction")
	t.Setenv("DB_MAX_CONNS", "8")
	t.Setenv("DB_MIN_CONNS", "0")
	handle, err := NewWithDirect(t.Context(), primary, direct)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	a, err := handle.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Release()
	b, err := handle.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Release()
	var firstPID, secondPID, value int
	for i := 0; i < 6; i++ {
		if err := a.QueryRow(t.Context(), `SELECT $1::int, pg_backend_pid()`, i).Scan(&value, &firstPID); err != nil || value != i {
			t.Fatalf("first pooled prepared query: value=%d error=%v", value, err)
		}
		if err := b.QueryRow(t.Context(), `SELECT $1::int, pg_backend_pid()`, i).Scan(&value, &secondPID); err != nil || value != i {
			t.Fatalf("second pooled prepared query: value=%d error=%v", value, err)
		}
		if firstPID != secondPID {
			t.Fatal("fixture must multiplex both clients over one server connection")
		}
	}
	session, err := handle.ConnectSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close(context.Background())
	if _, err := session.Exec(t.Context(), `SELECT pg_advisory_lock(1849137, 1)`); err != nil {
		t.Fatal(err)
	}
	var locked bool
	if err := b.QueryRow(t.Context(), `SELECT pg_try_advisory_lock(1849137, 1)`).Scan(&locked); err != nil || locked {
		t.Fatalf("direct lock must remain held across pooled transactions: locked=%t error=%v", locked, err)
	}
	if err := session.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	tx, err := b.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if err := tx.QueryRow(t.Context(), `SELECT pg_try_advisory_xact_lock(1849137, 1)`).Scan(&locked); err != nil || !locked {
		t.Fatalf("closing the direct session must release its lock: locked=%t error=%v", locked, err)
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	probe, err := handle.ConnectSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close(context.Background())
	if _, err := probe.Exec(t.Context(), `SET statement_timeout=0`); err != nil {
		t.Fatal(err)
	}
	if err := verifyPooledTimeouts(t.Context(), probe); err == nil {
		t.Fatal("a pooled connection without statement timeout enforcement was accepted")
	}
}
