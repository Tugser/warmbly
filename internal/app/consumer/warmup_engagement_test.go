package jobs

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

type pendingEngagementStub struct {
	repository.WarmupEngagementRepository
	due []repository.PendingEngagement
}

func (r *pendingEngagementStub) ClaimDuePendingEngagements(context.Context, int) ([]repository.PendingEngagement, error) {
	due := r.due
	r.due = nil
	return due, nil
}

func TestDelayedWarmupEngagementRechecksReceivingAndAssignment(t *testing.T) {
	mode := models.TestParticipationDiagnostic
	actions := []string{models.WarmupActionMarkRead, models.WarmupActionMarkImportant, models.WarmupActionStar}
	for _, tc := range []struct {
		name    string
		mode    models.TestParticipationMode
		receive bool
		status  string
		want    bool
	}{
		{"receiving enabled", mode, true, "active", true},
		{"worker reassigned during dwell", mode, true, "active", true},
		{"unassigned during dwell", mode, true, "active", false},
		{"receiving revoked during dwell", mode, false, "active", false},
		{"stopped during dwell", models.TestParticipationOff, true, "active", false},
		{"inactive during dwell", mode, true, "inactive", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			worker := uuid.New()
			account := &models.Email{ID: uuid.New(), WorkerID: &worker, TestMode: &tc.mode, TestReceiveEnabled: tc.receive, Status: tc.status}
			payload, err := json.Marshal(models.WarmupEmailAction{EmailID: account.ID, Actions: actions, DelaySeconds: 120})
			if err != nil {
				t.Fatal(err)
			}
			pending := &pendingEngagementStub{due: []repository.PendingEngagement{{ID: uuid.New(), EmailAccountID: account.ID, Payload: payload}}}
			if tc.name == "worker reassigned during dwell" {
				worker = uuid.New()
				account.WorkerID = &worker
			} else if tc.name == "unassigned during dwell" {
				account.WorkerID = nil
			}
			pub := &backfillPublisher{}
			s := JobsService{WarmupEngagementRepo: pending, Publisher: pub, EmailRepository: backfillEmailRepo{account: account}}
			if err := s.drainDueEngagements(t.Context()); err != nil {
				t.Fatal(err)
			}
			if !tc.want {
				if len(pub.actions) != 0 {
					t.Fatal("revoked engagement was published", pub.actions)
				}
				return
			}
			if len(pub.actions) != 1 || !slices.Equal(pub.actions[0].Actions, actions) || pub.actions[0].DelaySeconds != 0 || pub.workers[0] != worker {
				t.Fatal("engagement lost actions, elapsed dwell or current worker", pub.actions, pub.workers)
			}
			if err := s.drainDueEngagements(t.Context()); err != nil || len(pub.actions) != 1 {
				t.Fatal("engagement replayed after being claimed", err)
			}
		})
	}
}

func TestHumanizeFireAt_DefersNightToMorning(t *testing.T) {
	// 3am local — a read at this hour is a bot signature.
	night := time.Date(2026, 1, 1, 3, 0, 0, 0, time.UTC)
	got := humanizeFireAt(night, "UTC")
	h := got.Hour()
	if h < 7 || h > 9 {
		t.Errorf("night fire time not deferred into 07:30-09:30 window: hour=%d (%v)", h, got)
	}
	if !got.After(night) {
		t.Errorf("deferred time should be later than the night time: %v", got)
	}
}

func TestHumanizeFireAt_KeepsDaytime(t *testing.T) {
	day := time.Date(2026, 1, 1, 14, 0, 0, 0, time.UTC)
	if got := humanizeFireAt(day, "UTC"); !got.Equal(day) {
		t.Errorf("daytime fire time should be unchanged, got %v", got)
	}
}

func TestHumanizeFireAt_LateNightRollsToNextMorning(t *testing.T) {
	// 23:00 should roll to the NEXT day's morning window.
	late := time.Date(2026, 1, 1, 23, 0, 0, 0, time.UTC)
	got := humanizeFireAt(late, "UTC")
	if got.Day() != 2 {
		t.Errorf("23:00 should defer to next day, got day %d (%v)", got.Day(), got)
	}
	if got.Hour() < 7 || got.Hour() > 9 {
		t.Errorf("deferred hour outside morning window: %d", got.Hour())
	}
}

func TestDwellSeconds_WithinBounds(t *testing.T) {
	for i := 0; i < 500; i++ {
		d := dwellSeconds(45, 3600, 1.0)
		if d < 45 || d > 3600 {
			t.Fatalf("dwell out of bounds: %d", d)
		}
	}
}

func TestDwellSeconds_HeavyTailedBias(t *testing.T) {
	// The u^2.2 curve should concentrate mass low: median well under the mid.
	var below int
	const n = 2000
	for i := 0; i < n; i++ {
		if dwellSeconds(45, 3600, 1.0) < (45+3600)/2 {
			below++
		}
	}
	// The u^2.2 curve puts its median around u=0.73, so ~73% of samples fall
	// below the midpoint (vs 50% for a uniform draw). Assert clearly above
	// uniform without hugging the exact figure.
	if below < n*13/20 {
		t.Errorf("expected heavy-tailed (>=65%% below midpoint), got %d/%d", below, n)
	}
}

func TestEngagementPlan_AlwaysFoldersFirst(t *testing.T) {
	set := models.WarmupEngagementSettings{
		SpamRescueRate: 85, MarkReadRate: 95, MarkImportantRate: 30, StarRate: 20,
		MinDwellSeconds: 45, MaxDwellSeconds: 3600,
	}
	for i := 0; i < 100; i++ {
		actions, delay := engagementPlan(uuid.New(), set)
		if len(actions) == 0 || actions[0] != "move_to_warmbly" {
			t.Fatalf("foldering must always be first: %v", actions)
		}
		if delay < 45 || delay > 3600 {
			t.Fatalf("delay out of bounds: %d", delay)
		}
	}
}

func TestEngagementPlan_SometimesNeglects(t *testing.T) {
	set := models.WarmupEngagementSettings{
		SpamRescueRate: 0, MarkReadRate: 100, MarkImportantRate: 100, StarRate: 100,
		MinDwellSeconds: 45, MaxDwellSeconds: 3600,
	}
	// With all positive rates at 100%, a plan with ONLY move_to_warmbly can only
	// happen via the neglect path. Over many mailboxes/messages it must occur.
	neglected := 0
	for i := 0; i < 2000; i++ {
		actions, _ := engagementPlan(uuid.New(), set)
		if len(actions) == 1 {
			neglected++
		}
	}
	if neglected == 0 {
		t.Error("engagement is never neglected — every message gets perfect engagement")
	}
}

func TestSentFolderCopy(t *testing.T) {
	cases := []struct {
		name string
		msg  models.EmailMessageStoreData
		want bool
	}{
		{"resolved sent folder", models.EmailMessageStoreData{Folder: models.FolderSent}, true},
		{"provider still reports sent", models.EmailMessageStoreData{ProviderFolder: models.FolderSent}, true},
		{"an inbox arrival is not our own copy", models.EmailMessageStoreData{Folder: models.FolderInbox}, false},
		{"warmup that landed in spam is not our own copy", models.EmailMessageStoreData{Flags: []string{"\\Junk"}}, false},
		{"a folderless event falls back to inbox", models.EmailMessageStoreData{}, false},
		{"a draft is not a sent copy", models.EmailMessageStoreData{Folder: models.FolderDrafts}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sentFolderCopy(&tc.msg); got != tc.want {
				t.Fatalf("sentFolderCopy() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRecoveryEngagementOnlyRaises(t *testing.T) {
	base := models.WarmupEngagementSettings{SpamRescueRate: 85, MarkReadRate: 95, MarkImportantRate: 30, StarRate: 20}
	got := recoveryEngagement(base)
	if got.SpamRescueRate != recoveryRescueRate || got.MarkImportantRate != recoveryImportantRate || got.MarkReadRate != 95 || got.StarRate != 20 {
		t.Fatalf("recovery engagement = %+v", got)
	}
	high := recoveryEngagement(models.WarmupEngagementSettings{MarkReadRate: 100, MarkImportantRate: 80})
	if high.MarkReadRate != 100 || high.MarkImportantRate != 80 {
		t.Fatalf("recovery engagement lowered a configured rate: %+v", high)
	}
	// Nearly every spam landing is rescued, but the persona bias keeps some mailboxes below every time.
	rescued := 0
	for i := 0; i < 4000; i++ {
		if actions, _ := engagementPlan(uuid.New(), got); slices.Contains(actions, models.WarmupActionRescueFromSpam) {
			rescued++
		}
	}
	if rescued < 3400 || rescued == 4000 {
		t.Fatalf("rescued %d of 4000; want most but not all", rescued)
	}
}

func TestRecoveryReplyChance(t *testing.T) {
	cases := []struct {
		rate       int
		recovering bool
		want       int
	}{
		{30, false, 30},
		{30, true, recoveryReplyRate},
		{80, true, 80},
		{0, true, 0},
	}
	for _, c := range cases {
		if got := recoveryReplyChance(c.rate, c.recovering); got != c.want {
			t.Errorf("recoveryReplyChance(%d, %v) = %d, want %d", c.rate, c.recovering, got, c.want)
		}
	}
}

func TestRollPctBiasNeverReachesCertainty(t *testing.T) {
	misses := 0
	for i := 0; i < 5000; i++ {
		if !rollPct(95, 1.2) {
			misses++
		}
	}
	if misses == 0 {
		t.Fatal("a biased 95% chance succeeded every time")
	}
	for i := 0; i < 1000; i++ {
		if !rollPct(100, 0.8) {
			t.Fatal("an explicit 100% chance missed")
		}
	}
}

type activeCampaignsStub struct {
	repository.CampaignRepository
	n int
}

func (r activeCampaignsStub) CountActiveCampaignsForAccount(context.Context, uuid.UUID) (int, error) {
	return r.n, nil
}

func TestCampaignSenderGetsBoostedEngagement(t *testing.T) {
	ctx := context.Background()
	if !(&JobsService{CampaignRepo: activeCampaignsStub{n: 1}}).senderGetsRecoveryEngagement(ctx, uuid.New()) {
		t.Fatal("a sender backing a live campaign should get the reply boost")
	}
	if (&JobsService{CampaignRepo: activeCampaignsStub{}}).senderGetsRecoveryEngagement(ctx, uuid.New()) {
		t.Fatal("a healthy sender with no live campaign should not")
	}
}
