package mailmanager

import (
	"errors"
	"net"
	"testing"
	"time"

	goimap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

func TestRecoveryProbeTransientReplacementStaysLoadedAndRecovers(t *testing.T) {
	t.Setenv("DEPLOYMENT_MODE", "self_hosted")
	events := 0
	m := NewMailManager(func(models.JobEventType, string, any) error { events++; return nil }, nil, nil, nil, nil, nil, nil)
	d := smtpPayload()
	stamp := time.Now().Add(-time.Hour)
	d.Sync.State.LastSyncedAt = &stamp
	if err := m.AddWMail(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	old := m.Get(d.ID)
	t.Cleanup(func() { m.Terminate(d.ID) })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr, port := ln.Addr().String(), ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	retry := *d
	s := *d.SmtpImap
	creds := *s.Credentials
	creds.IMAP = &models.Service{Host: "127.0.0.1", Port: port, Username: "local", Password: "local", Security: models.MailSecurityNone}
	s.Credentials = &creds
	retry.SmtpImap, retry.ImapSync = &s, true
	if err := m.AddWMail(t.Context(), &retry); err != nil {
		t.Fatalf("temporary IMAP outage blocked the replacement: %v", err)
	}
	next := m.Get(d.ID)
	if next == nil || next == old || !m.Has(d.ID) || old.Ctx.Err() == nil || !next.SameExecution(&retry) {
		t.Fatal("replacement did not stop the old execution and install the retryable mailbox")
	}
	if _, ok := old.BeginExecution(); ok {
		t.Fatal("stopped pointer admitted work")
	}
	if old.SameExecution(d) || old.SameExecution(&retry) {
		t.Fatal("stopped pointer bypasses reload on republish")
	}
	old.CaptureError(errx.ErrMailServerUnreachable)
	if events != 0 {
		t.Fatal("retired pointer emitted a fresh transport report")
	}
	mem := imapmemserver.New()
	u := imapmemserver.NewUser("local", "local")
	if err := u.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	mem.AddUser(u)
	ln, err = net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	srv := imapserver.New(&imapserver.Options{Caps: goimap.CapSet{goimap.CapIMAP4rev1: {}}, InsecureAuth: true, NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
		return mem.NewSession(), nil, nil
	}})
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()
	if err := m.AddWMail(t.Context(), &retry); err != nil {
		t.Fatal(err)
	}
	if m.Get(d.ID) != next || next.Ctx.Err() != nil {
		t.Fatal("republish replaced a usable mailbox while sync was retrying")
	}
	resumed := retry
	next.ResumeInto(&resumed)
	if resumed.Sync.State.LastSyncedAt == nil || !resumed.Sync.State.LastSyncedAt.Equal(stamp) || resumed.Sync.State.BackfillSynced != 7 {
		t.Fatal("retry lost previous sync evidence")
	}
	if err := next.SyncMail(t.Context()); err != nil {
		t.Fatalf("loaded mailbox could not recover without replacement: %v", err)
	}
}

func TestRecoveryProbeRejectedReplacementStopsOldExecution(t *testing.T) {
	m := NewMailManager(func(models.JobEventType, string, any) error { return nil }, nil, nil, nil, nil, nil, nil)
	d := smtpPayload()
	if err := m.AddWMail(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Terminate(d.ID) })
	old := m.Get(d.ID)
	rejected := *d
	rejected.SmtpImap = nil
	err := m.AddWMail(t.Context(), &rejected)
	var failure *errx.MailError
	if !errors.As(err, &failure) || failure.Code != errx.MailErrorCodeInvalidCredentials || m.Get(d.ID) != old {
		t.Fatalf("invalid replacement bypassed credential failure: %v", err)
	}
	if _, ok := old.BeginExecution(); ok {
		t.Fatal("rejected replacement retained executable old credentials")
	}
	if old.SameExecution(d) || old.SameExecution(&rejected) {
		t.Fatal("stopped pointer bypasses credential reload")
	}
	if err := m.AddWMail(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	if next := m.Get(d.ID); next == nil || next == old || !next.SameExecution(d) {
		t.Fatal("valid reoffer did not recover the stopped execution")
	}
}
