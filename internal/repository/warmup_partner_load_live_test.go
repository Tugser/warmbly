package repository

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/config"
	"github.com/warmbly/warmbly/internal/models"
)

// The shared seven-day snapshot hands out the same candidates and counts as
// the exact read, and today's inbound cap still applies the moment it fills.
func TestLiveWarmupPartnerWeekCacheMatchesExactReadAndKeepsTheCapLive(t *testing.T) {
	f := newPartnerOrgFixture(t)
	ctx := context.Background()
	exact := &warmupRepository{db: f.pool}
	cached := &warmupRepository{db: f.pool, partnerWeeks7d: newPartnerWeekCache(time.Hour)}

	other := &partnerOrgFixture{pool: f.pool, sender: f.outside, exec: f.exec}
	for i := 0; i < 3; i++ {
		other.warmed(t, f.sibling)
	}
	f.received(t, f.outside, f.sibling, time.Now().Add(-2*time.Hour))
	t.Cleanup(func() {
		f.exec(`DELETE FROM warmup_tokens WHERE sender_account_id = $1`, f.outside)
		f.exec(`DELETE FROM tasks WHERE email_account_id = $1`, f.outside)
	})

	assertSame := func(stage string) []models.WarmupPartnerCandidate {
		t.Helper()
		want, err := exact.WarmupPartnerCandidates(ctx, "premium", f.sender)
		if err != nil {
			t.Fatal(err)
		}
		got, err := cached.WarmupPartnerCandidates(ctx, "premium", f.sender)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(want) {
			t.Fatalf("%s: cached %d candidates, exact %d", stage, len(got), len(want))
		}
		for _, w := range want {
			g, ok := candidateByID(got, w.ID)
			g.OrganizationID, w.OrganizationID = nil, nil
			if !ok || g != w {
				t.Fatalf("%s: cached %+v, exact %+v", stage, g, w)
			}
		}
		return got
	}
	got := assertSame("warm")
	if c, ok := candidateByID(got, f.outside); !ok || c.Sent7d != 3 || c.Received7d != 1 {
		t.Fatalf("outside partner = %+v, want sent/received 3/1", c)
	}

	// Filling the sibling's day removes it at once, snapshot or not.
	for i := 0; i < config.WarmupInboundDailyFloor; i++ {
		f.received(t, f.sibling, f.outside, time.Now().Add(-time.Minute))
	}
	got, err := cached.WarmupPartnerCandidates(ctx, "premium", f.sender)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := candidateByID(got, f.sibling); ok {
		t.Fatal("a mailbox at today's inbound cap is still offered from the cached path")
	}

	// A reserved send does not count against its own recipient.
	f.exec(`DELETE FROM warmup_received WHERE email_account_id = $1`, f.sibling)
	f.exec(`DELETE FROM warmup_tokens WHERE recipient_account_id = $1`, f.sibling)
	for i := 0; i < config.WarmupInboundDailyFloor-1; i++ {
		other.warmed(t, f.sibling)
	}
	reserved := uuid.New()
	f.exec(`INSERT INTO tasks (id, task_type, email_account_id, status, message_id) VALUES ($1, 'warmup', $2, 'active', '')`, reserved, f.sender)
	f.exec(`INSERT INTO warmup_tokens (token, task_id, sender_account_id, recipient_account_id, created_at)
	        VALUES (gen_random_uuid(), $1, $2, $3, NOW())`, reserved, f.sender, f.sibling)
	t.Cleanup(func() { f.exec(`DELETE FROM tasks WHERE id = $1`, reserved) })
	for _, repo := range []*warmupRepository{exact, cached} {
		planned, err := repo.WarmupPartnerCandidates(ctx, "premium", f.sender)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := candidateByID(planned, f.sibling); ok {
			t.Fatal("a full inbox is offered to a new send")
		}
		executing, err := repo.WarmupExecutionCandidates(ctx, "premium", f.sender, reserved)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := candidateByID(executing, f.sibling); !ok {
			t.Fatal("a reserved send was counted against its own recipient")
		}
	}
}

func TestPartnerInboundCapMatchesTheSQLRule(t *testing.T) {
	for _, tc := range []struct{ sent, share, want int }{
		{0, 100, config.WarmupInboundDailyFloor},
		{35, 100, max(config.WarmupInboundDailyFloor, 5*config.WarmupInboundDailyMultiple)},
		{36, 100, max(config.WarmupInboundDailyFloor, 6*config.WarmupInboundDailyMultiple)},
		{10_000, 100, config.WarmupInboundDailyCeiling},
		{0, 75, config.WarmupInboundDailyFloor * 75 / 100},
	} {
		if got := partnerInboundCap(tc.sent, tc.share); got != tc.want {
			t.Fatalf("cap(%d, %d%%) = %d, want %d", tc.sent, tc.share, got, tc.want)
		}
	}
}

// A caller that gives up does not cancel the shared refresh, and an expired
// snapshot is served while one refresh replaces it.
func TestLiveWarmupPartnerWeekCacheRefreshesDetachedAndServesStale(t *testing.T) {
	f := newPartnerOrgFixture(t)
	c := newPartnerWeekCache(time.Hour)
	repo := &warmupRepository{db: f.pool, partnerWeeks7d: c}
	ids := []uuid.UUID{f.sender, f.sibling, f.outside}

	gone, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = repo.partnerWeeks(gone, ids)
	got, err := repo.partnerWeeks(context.Background(), ids)
	if err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	landed := c.data != nil
	c.mu.Unlock()
	if !landed {
		t.Fatal("the refresh a cancelled caller started did not land")
	}
	exact, err := (&warmupRepository{db: f.pool}).partnerWeeks(context.Background(), ids)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if got[id] != exact[id] {
			t.Fatalf("%s: snapshot %+v, exact %+v", id, got[id], exact[id])
		}
	}

	c.mu.Lock()
	c.at = time.Now().Add(-2 * time.Hour)
	before := c.data
	c.mu.Unlock()
	stale, err := repo.partnerWeeks(context.Background(), ids)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.ValueOf(stale).Pointer() != reflect.ValueOf(before).Pointer() {
		t.Fatal("an expired snapshot was not served while it refreshed")
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		c.mu.Lock()
		refreshed := time.Since(c.at) < time.Minute && c.refreshing == nil
		c.mu.Unlock()
		if refreshed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the expired snapshot was never replaced")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// lost records n handed-off warmup sends from sender to recipient, made age
// ago, that the recipient's sync never verified.
func (f *partnerOrgFixture) lost(t *testing.T, sender, recipient uuid.UUID, n int, age time.Duration) {
	t.Helper()
	for range n {
		taskID := uuid.New()
		f.exec(`INSERT INTO tasks (id, task_type, email_account_id, status, message_id)
		        VALUES ($1, 'warmup', $2, 'completed', '')`, taskID, sender)
		f.exec(`INSERT INTO warmup_tokens (token, task_id, sender_account_id, recipient_account_id, sent_message_id, created_at)
		        VALUES (gen_random_uuid(), $1, $2, $3, $4, $5)`,
			taskID, sender, recipient, "<"+taskID.String()+"@test.local>", time.Now().Add(-age))
	}
}

// A recipient that verified none of the pool's mail from two workspaces leaves
// the set on both read paths, and one verified arrival brings it back. Losses
// from a single workspace, or too young to judge, take nobody out.
func TestLiveWarmupPartnerCandidatesDropAnUnreachableRecipient(t *testing.T) {
	f := newPartnerOrgFixture(t)
	ctx := context.Background()
	exact := &warmupRepository{db: f.pool}
	t.Cleanup(func() {
		f.exec(`DELETE FROM warmup_tokens WHERE recipient_account_id = $1`, f.sibling)
		f.exec(`DELETE FROM tasks WHERE email_account_id IN ($1, $2)`, f.sender, f.outside)
	})
	offered := func(stage string, want bool) models.WarmupPartnerCandidate {
		t.Helper()
		for _, repo := range []*warmupRepository{exact, {db: f.pool, partnerWeeks7d: newPartnerWeekCache(0)}} {
			cands, err := repo.WarmupPartnerCandidates(ctx, "premium", f.sender)
			if err != nil {
				t.Fatal(err)
			}
			c, ok := candidateByID(cands, f.sibling)
			if ok != want {
				t.Fatalf("%s: recipient offered = %v, want %v (cached=%v)", stage, ok, want, repo.partnerWeeks7d != nil)
			}
			if ok {
				return c
			}
		}
		return models.WarmupPartnerCandidate{}
	}
	grace := time.Duration(config.WarmupRecipientArrivalGraceHours) * time.Hour

	sample := config.WarmupRecipientUnreachableMinDelivered
	f.lost(t, f.sender, f.sibling, sample, grace+time.Hour)
	if c := offered("losses from one workspace", true); c.ArrivalRate() >= 1 {
		t.Fatalf("one workspace's losses still weigh the draw: %+v", c)
	}
	f.lost(t, f.outside, f.sibling, 3, grace/2)
	offered("a second workspace's losses inside the grace", true)
	f.lost(t, f.outside, f.sibling, 1, grace+time.Hour)
	offered("losses from two workspaces", false)

	f.exec(`UPDATE warmup_tokens SET consumed_at = NOW()
	        WHERE token = (SELECT token FROM warmup_tokens WHERE recipient_account_id = $1 AND sender_account_id = $2 ORDER BY created_at DESC LIMIT 1)`,
		f.sibling, f.outside)
	c := offered("one verified arrival inside the grace", true)
	if c.Delivered7d != sample+2 || c.Arrived7d != 1 {
		t.Fatalf("delivered/arrived = %d/%d, want %d/1", c.Delivered7d, c.Arrived7d, sample+2)
	}
}

// A reply's pre-check and the send reservation read one rule, so a reply the
// reservation would refuse is known to be refused before it holds the slot.
func TestLiveWarmupInboxHasRoomMatchesTheReservation(t *testing.T) {
	f := newPartnerOrgFixture(t)
	ctx := context.Background()
	repo := &warmupRepository{db: f.pool}
	room := func(pool string) bool {
		t.Helper()
		has, err := repo.WarmupInboxHasRoom(ctx, f.sibling, pool)
		if err != nil {
			t.Fatal(err)
		}
		return has
	}
	reserve := func(pool string) bool {
		t.Helper()
		var allowed bool
		if err := f.pool.QueryRow(ctx, warmupInboxRoomSQL, f.sibling, inboundSharePercent(pool), 0).Scan(&allowed); err != nil {
			t.Fatal(err)
		}
		return allowed
	}
	freeCap := config.WarmupInboundDailyFloor * config.WarmupFreeInboundSharePercent / 100
	for i := 0; i < freeCap-1; i++ {
		f.received(t, f.sibling, f.outside, time.Now().Add(-time.Second))
	}
	if !room("free") || !room("premium") {
		t.Fatal("an inbox one short of the free share has no room")
	}
	f.received(t, f.sibling, f.outside, time.Now().Add(-time.Second))
	if room("free") {
		t.Fatal("a free sender sees room past its share of the inbox")
	}
	if !reserve("free") {
		t.Fatal("the reservation refused a send already counted inside the free share")
	}
	if !room("premium") {
		t.Fatal("a paying sender lost the rest of the inbox's day")
	}
	for i := freeCap; i < config.WarmupInboundDailyFloor; i++ {
		f.received(t, f.sibling, f.outside, time.Now().Add(-time.Second))
	}
	if room("premium") || reserve("free") {
		t.Fatal("a full inbox still has room")
	}
}
