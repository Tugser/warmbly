package db

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestLiveLifetimeCapBoundsOutstandingInvitationsAndTesterPasswords(t *testing.T) {
	dsn := os.Getenv("WARMBLY_TEST_DB")
	if dsn == "" {
		t.Skip("WARMBLY_TEST_DB not set")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	schema := "lifetime_cap_" + uuid.New().String()[:8]
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = conn.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") }()
	if _, err := conn.Exec(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	const fixture = `
 CREATE TABLE organization_invitations (id text PRIMARY KEY, expires_at timestamptz NOT NULL, created_at timestamptz NOT NULL DEFAULT now());
 CREATE TABLE users (id text PRIMARY KEY, password_expires_at timestamptz, updated_at timestamptz NOT NULL DEFAULT now());
 INSERT INTO organization_invitations VALUES
   ('week', now() + interval '7 days', now() - interval '1 hour'),
   ('reinvited', now() + interval '47 hours', now() - interval '30 days'),
   ('expired', now() - interval '1 day', now() - interval '8 days');
 INSERT INTO users (id, password_expires_at) VALUES
   ('month', now() + interval '30 days'),
   ('short', now() + interval '6 hours'),
   ('ended', now() - interval '1 day'),
   ('member', NULL);
 `
	if _, err := conn.Exec(ctx, fixture); err != nil {
		t.Fatal(err)
	}
	up, err := migrationsFS.ReadFile("migrations/000326_cap_invitation_and_tester_lifetimes.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := conn.Exec(ctx, string(up)); err != nil {
			t.Fatal(err)
		}
	}
	horizon := 48*time.Hour + time.Minute
	check := func(table, column, id string, want func(left *time.Duration) bool) {
		t.Helper()
		var left *time.Duration
		var at *time.Time
		if err := conn.QueryRow(ctx, "SELECT "+column+" FROM "+table+" WHERE id = $1", id).Scan(&at); err != nil {
			t.Fatal(err)
		}
		if at != nil {
			d := time.Until(*at)
			left = &d
		}
		if !want(left) {
			t.Errorf("%s %s: %s left = %v", table, id, column, left)
		}
	}
	within := func(lo, hi time.Duration) func(*time.Duration) bool {
		return func(d *time.Duration) bool { return d != nil && *d > lo && *d <= hi }
	}
	check("organization_invitations", "expires_at", "week", within(47*time.Hour, horizon))
	check("organization_invitations", "expires_at", "reinvited", within(46*time.Hour, 47*time.Hour))
	check("organization_invitations", "expires_at", "expired", within(-25*time.Hour, -23*time.Hour))
	check("users", "password_expires_at", "month", within(47*time.Hour, horizon))
	check("users", "password_expires_at", "short", within(5*time.Hour, 6*time.Hour))
	check("users", "password_expires_at", "ended", within(-25*time.Hour, -23*time.Hour))
	check("users", "password_expires_at", "member", func(d *time.Duration) bool { return d == nil })
}
