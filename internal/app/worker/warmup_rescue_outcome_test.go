package worker

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/app/worker/wmail"
	"github.com/warmbly/warmbly/internal/infrastructure/codec"
	"github.com/warmbly/warmbly/internal/models"
)

func rescueOutcome(t *testing.T, arrived string, actions ...string) string {
	t.Helper()
	mail := &wmail.WMail{SmtpImapData: &wmail.SmtpImapData{
		ImapClient: &callLog{},
		Mailboxes:  []*models.Mailbox{{Name: "INBOX", UIDValidity: 1}, {Name: "Junk", Attrs: []string{"\\Junk"}, UIDValidity: 2}},
	}}
	validity := uint32(1)
	if arrived == "Junk" {
		validity = 2
	}
	action := models.WarmupEmailAction{EmailID: uuid.New(), UID: 41, MailboxFolder: arrived, MailboxUIDValidity: validity,
		Placement: models.WarmupPlacementFolder, TargetFolder: "Warmbly", Actions: actions}
	outcome := ""
	if err := (&WorkerService{}).runImapWarmupActions(context.Background(), mail, action, &outcome); err != nil {
		t.Fatal(err)
	}
	return outcome
}

func TestImapRescueReportsWhatHappened(t *testing.T) {
	for _, tc := range []struct {
		name, arrived, want string
		actions             []string
	}{
		{"moved out of junk", "Junk", models.WarmupRescueMoved, []string{models.WarmupActionRescueFromSpam}},
		{"not in junk", "INBOX", models.WarmupRescueAlreadyOut, []string{models.WarmupActionRescueFromSpam}},
		{"filed out first", "Junk", models.WarmupRescueAlreadyOut, []string{models.WarmupActionFile, models.WarmupActionRescueFromSpam}},
	} {
		if got := rescueOutcome(t, tc.arrived, tc.actions...); got != tc.want {
			t.Fatalf("%s: outcome %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestRescueOutcomeIsReportedOnlyWhenAsked(t *testing.T) {
	account := uuid.New()
	run := func(internalID string) []models.JobEvent {
		w := newLoadingWorker(&capturedEvents{})
		w.mailManager.Emails[account] = &wmail.WMail{EmailType: models.InboxProviderSMTPIMAP, SmtpImapData: &wmail.SmtpImapData{
			ImapClient: &callLog{},
			Mailboxes:  []*models.Mailbox{{Name: "INBOX", UIDValidity: 1}, {Name: "Junk", Attrs: []string{"\\Junk"}, UIDValidity: 2}},
		}}
		bus := &filingBus{}
		w.Bus, w.Codec = bus, codec.NewJSON()
		action := models.WarmupEmailAction{EmailID: account, UID: 41, MailboxFolder: "Junk", MailboxUIDValidity: 2,
			InternalID: internalID, Actions: []string{models.WarmupActionRescueFromSpam}}
		if err := w.HandleWarmupAction(context.Background(), action); err != nil {
			t.Fatal(err)
		}
		return bus.events
	}
	if events := run(""); len(events) != 0 {
		t.Fatalf("a rescue nobody asked about was reported: %+v", events)
	}
	events := run(uuid.NewString())
	if len(events) != 1 || events[0].Type != models.JobEventTypeWarmupRescued {
		t.Fatalf("the rescue outcome was not reported: %+v", events)
	}
}
