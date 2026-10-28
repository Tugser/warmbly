package fleetnode

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
)

// ErrRolloutConflict means another backend changed the release state first.
var ErrRolloutConflict = errors.New("fleet release state changed concurrently; retry")

// ErrNoRollout means there is no rollout to act on.
var ErrNoRollout = errors.New("no fleet rollout is in progress")

func (s *Service) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// StartRollout moves the fleet to target in stages: a canary first, then the
// policy's waves, each admitted only after the previous one has baked.
// Starting the rollout already running for target is a no-op.
func (s *Service) StartRollout(ctx context.Context, target string, digests map[string]string, policy models.FleetRolloutPolicy) (*models.FleetReleaseState, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, errors.New("tag required")
	}
	if s.variant != "" {
		target = strings.TrimSuffix(target, s.variant)
	}
	if err := s.CheckTarget(target); err != nil {
		return nil, err
	}
	prev, err := s.settings.GetRelease(ctx)
	if err != nil {
		return nil, err
	}
	if prev != nil && prev.Rollout.Running() && prev.Rollout.Target == target {
		return prev, nil
	}
	now := s.clock()
	next := &models.FleetReleaseState{Channel: models.FleetChannelPinned, ResolvedAt: now, Source: "rollout"}
	if prev != nil {
		next.Tag = prev.Tag
		next.ResolvedAt = prev.ResolvedAt
		next.Source = prev.Source
	}
	if next.Tag == target {
		next.Rollout = nil
		return s.commit(ctx, prev, next)
	}
	clean := map[string]string{}
	for k, v := range digests {
		if d := validDigest(strings.TrimSpace(v)); d != "" {
			clean[strings.TrimSpace(k)] = d
		}
	}
	r := &models.FleetRollout{
		Target:         target,
		From:           next.Tag,
		Digests:        clean,
		Policy:         policy.Normalize(),
		Stage:          models.FleetRolloutCanary,
		StartedAt:      now,
		StageStartedAt: now,
	}
	candidates, err := s.rolloutCandidates(ctx, target)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return s.commit(ctx, prev, finish(next, r, now))
	}
	n := r.Policy.Canary
	if n > len(candidates) {
		n = len(candidates)
	}
	for _, c := range candidates[:n] {
		r.Admitted = append(r.Admitted, c.ID)
	}
	next.Rollout = r
	return s.commit(ctx, prev, next)
}

// AdvanceRollout evaluates the running rollout once: it halts on a failed or
// silent node, admits the next stage once the current one has baked, and
// finishes when every node is admitted. force skips the bake, never the checks.
func (s *Service) AdvanceRollout(ctx context.Context, force bool) (*models.FleetReleaseState, error) {
	prev, err := s.settings.GetRelease(ctx)
	if err != nil {
		return nil, err
	}
	if prev == nil || !prev.Rollout.Running() {
		return prev, nil
	}
	nodes, err := s.nodes.List(ctx, "")
	if err != nil {
		return nil, err
	}
	now := s.clock()
	r := *prev.Rollout
	r.Admitted = append([]uuid.UUID(nil), prev.Rollout.Admitted...)
	next := *prev
	next.Rollout = &r

	if reason, done := s.stageVerdict(&r, nodes, now); reason != "" {
		r.Stage = models.FleetRolloutHalted
		r.HaltReason = reason
		log.Printf("fleet: rollout to %s halted: %s", r.Target, reason)
		return s.commit(ctx, prev, &next)
	} else if !done {
		return prev, nil
	} else if !force && now.Sub(r.StageStartedAt) < time.Duration(r.Policy.BakeSeconds)*time.Second {
		return prev, nil
	}

	candidates, err := s.rolloutCandidates(ctx, r.Target)
	if err != nil {
		return nil, err
	}
	remaining := make([]models.FleetNode, 0, len(candidates))
	for _, c := range candidates {
		if !r.IsAdmitted(c.ID) {
			remaining = append(remaining, c)
		}
	}
	wave := 0
	if r.Stage == models.FleetRolloutWave {
		wave = r.Wave + 1
	}
	total := len(r.Admitted) + len(remaining)
	for ; wave < len(r.Policy.Waves) && len(remaining) > 0; wave++ {
		want := (total*r.Policy.Waves[wave] + 99) / 100
		added := 0
		for len(r.Admitted) < want && len(remaining) > 0 {
			r.Admitted = append(r.Admitted, remaining[0].ID)
			remaining = remaining[1:]
			added++
		}
		if added > 0 {
			r.Stage = models.FleetRolloutWave
			r.Wave = wave
			r.StageStartedAt = now
			log.Printf("fleet: rollout to %s admitted wave %d (%d of %d nodes)", r.Target, wave+1, len(r.Admitted), total)
			return s.commit(ctx, prev, &next)
		}
	}
	log.Printf("fleet: rollout to %s finished on %d nodes", r.Target, len(r.Admitted))
	return s.commit(ctx, prev, finish(&next, &r, now))
}

// HaltRollout stops admitting nodes and sends admitted ones back to From.
func (s *Service) HaltRollout(ctx context.Context, reason string) (*models.FleetReleaseState, error) {
	prev, err := s.settings.GetRelease(ctx)
	if err != nil {
		return nil, err
	}
	if prev == nil || !prev.Rollout.Running() {
		return nil, ErrNoRollout
	}
	r := *prev.Rollout
	r.Stage = models.FleetRolloutHalted
	if reason = strings.TrimSpace(reason); reason == "" {
		reason = "halted by an operator"
	}
	r.HaltReason = reason
	next := *prev
	next.Rollout = &r
	return s.commit(ctx, prev, &next)
}

// AbortRollout discards the rollout, running or halted, leaving Tag at From.
func (s *Service) AbortRollout(ctx context.Context) (*models.FleetReleaseState, error) {
	prev, err := s.settings.GetRelease(ctx)
	if err != nil {
		return nil, err
	}
	if prev == nil || prev.Rollout == nil {
		return nil, ErrNoRollout
	}
	next := *prev
	next.Rollout = nil
	return s.commit(ctx, prev, &next)
}

// RunRolloutLoop advances any running rollout every interval until ctx ends.
func (s *Service) RunRolloutLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.AdvanceRollout(ctx, false); err != nil && !errors.Is(err, ErrRolloutConflict) {
				log.Printf("fleet: rollout advance: %v", err)
			}
		}
	}
}

// stageVerdict returns a halt reason, or whether every admitted node has
// reached the target and stayed up for the bake.
func (s *Service) stageVerdict(r *models.FleetRollout, nodes []models.FleetNode, now time.Time) (string, bool) {
	byID := make(map[uuid.UUID]*models.FleetNode, len(nodes))
	for i := range nodes {
		byID[nodes[i].ID] = &nodes[i]
	}
	target := s.withVariant(r.Target)
	bake := time.Duration(r.Policy.BakeSeconds) * time.Second
	deadline := time.Duration(r.Policy.DeadlineSeconds) * time.Second
	elapsed := now.Sub(r.StageStartedAt)
	done := true
	for _, id := range r.Admitted {
		n := byID[id]
		switch {
		case n == nil:
			return fmt.Sprintf("node %s was removed during the rollout", id), false
		case n.PinnedVersion != "":
			continue
		case !n.LiveAt(now):
			return fmt.Sprintf("node %s (%s) stopped heartbeating", n.Name, id), false
		case n.UpdateFailed != "" && n.UpdateFailed == target:
			return fmt.Sprintf("node %s (%s) rolled back from %s on the host", n.Name, id, target), false
		case n.Version != target:
			if elapsed > deadline {
				return fmt.Sprintf("node %s (%s) did not reach %s within %s", n.Name, id, target, deadline), false
			}
			done = false
		case n.Usage.UptimeSeconds == nil || time.Duration(*n.Usage.UptimeSeconds)*time.Second < bake:
			if elapsed > deadline+2*bake {
				return fmt.Sprintf("node %s (%s) kept restarting on %s", n.Name, id, target), false
			}
			done = false
		}
	}
	return "", done
}

// rolloutCandidates are live, unpinned nodes in admission order: workers
// first, then a stable shuffle per target so no machine always goes first.
func (s *Service) rolloutCandidates(ctx context.Context, target string) ([]models.FleetNode, error) {
	nodes, err := s.nodes.List(ctx, "")
	if err != nil {
		return nil, err
	}
	now := s.clock()
	out := make([]models.FleetNode, 0, len(nodes))
	for _, n := range nodes {
		if n.LiveAt(now) && n.PinnedVersion == "" {
			out = append(out, n)
		}
	}
	rank := func(n models.FleetNode) string {
		h := sha256.Sum256([]byte(target + "/" + n.ID.String()))
		return fmt.Sprintf("%x", h[:8])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Role == models.NodeRoleWorker) != (out[j].Role == models.NodeRoleWorker) {
			return out[i].Role == models.NodeRoleWorker
		}
		return rank(out[i]) < rank(out[j])
	})
	return out, nil
}

func finish(next *models.FleetReleaseState, r *models.FleetRollout, now time.Time) *models.FleetReleaseState {
	next.Tag = r.Target
	next.Channel = models.FleetChannelPinned
	next.ResolvedAt = now
	next.Source = "rollout"
	next.Rollout = nil
	return next
}

func (s *Service) commit(ctx context.Context, prev, next *models.FleetReleaseState) (*models.FleetReleaseState, error) {
	ok, err := s.settings.CompareAndSetRelease(ctx, prev, next)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrRolloutConflict
	}
	return next, nil
}
