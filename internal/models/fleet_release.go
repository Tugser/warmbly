package models

import (
	"time"

	"github.com/google/uuid"
)

// Admin settings keys for the fleet. Both live in `admin_settings` so every
// backend replica agrees and the values survive a restart.
const (
	// FleetSettingsKeyRelease holds the resolved release the fleet should be
	// running (FleetReleaseState).
	FleetSettingsKeyRelease = "fleet.release"
	// FleetSettingsKeyJoinToken holds the bcrypt-style hash of the instance
	// join token. The token itself is shown once, at issue time, and never
	// stored.
	FleetSettingsKeyJoinToken = "fleet.join_token"
)

// ReleaseChannelStable and friends name which GitHub releases the fleet
// follows.
const (
	FleetChannelStable = "stable"
	FleetChannelDev    = "dev"
	// FleetChannelPinned freezes the fleet at whatever Tag currently says.
	// Nodes still self-update TO that tag; they just stop following new ones.
	FleetChannelPinned = "pinned"
)

// FleetReleaseState is the answer to "what version should my nodes be running".
//
// One tag covers every role: worker and consumer ship from the same repository
// and the same release, so a node needs the tag and already knows its own
// image name. Keeping it to one value is what makes "everything is on the same
// version" checkable at a glance instead of a per-role matrix.
type FleetReleaseState struct {
	Channel string `json:"channel"`
	// Tag is the resolved release, e.g. "v1.4.2". Empty means nothing has been
	// resolved yet, which every consumer of this must read as "no opinion"
	// rather than "downgrade to nothing".
	Tag        string    `json:"tag"`
	ResolvedAt time.Time `json:"resolved_at"`
	// Source records how Tag was set, so an operator can tell an automatic
	// resolution from a manual pin.
	Source string `json:"source,omitempty"`
	// Rollout is a staged move to a newer tag, nil when none is in progress.
	Rollout *FleetRollout `json:"rollout,omitempty"`
}

// DesiredVersion is the tag nodes should converge on, or "" when the control
// plane has no opinion.
func (s *FleetReleaseState) DesiredVersion() string {
	if s == nil {
		return ""
	}
	return s.Tag
}

// Rollout stages. A rollout admits nodes stage by stage and only moves on
// once every admitted node has run the target healthily for the bake time.
const (
	FleetRolloutCanary = "canary"
	FleetRolloutWave   = "wave"
	FleetRolloutHalted = "halted"
)

// FleetRolloutPolicy shapes a staged rollout.
type FleetRolloutPolicy struct {
	// Canary is how many nodes go first, alone.
	Canary int `json:"canary"`
	// Waves are cumulative percentages of the fleet admitted after the canary.
	Waves []int `json:"waves"`
	// BakeSeconds is how long every admitted node must run the target before
	// the next stage is admitted.
	BakeSeconds int `json:"bake_seconds"`
	// DeadlineSeconds is how long an admitted node has to report the target
	// before the rollout halts.
	DeadlineSeconds int `json:"deadline_seconds"`
}

// DefaultFleetRolloutPolicy is one canary, then everyone, with a 30 minute bake.
func DefaultFleetRolloutPolicy() FleetRolloutPolicy {
	return FleetRolloutPolicy{Canary: 1, Waves: []int{100}, BakeSeconds: 1800, DeadlineSeconds: 900}
}

// Normalize fills unset fields with the defaults and bounds the rest.
func (p FleetRolloutPolicy) Normalize() FleetRolloutPolicy {
	def := DefaultFleetRolloutPolicy()
	if p.Canary <= 0 {
		p.Canary = def.Canary
	}
	waves := make([]int, 0, len(p.Waves)+1)
	last := 0
	for _, w := range p.Waves {
		if w > last && w <= 100 {
			waves = append(waves, w)
			last = w
		}
	}
	if last < 100 {
		waves = append(waves, 100)
	}
	p.Waves = waves
	if p.BakeSeconds <= 0 {
		p.BakeSeconds = def.BakeSeconds
	}
	if p.DeadlineSeconds <= 0 {
		p.DeadlineSeconds = def.DeadlineSeconds
	}
	return p
}

// FleetRollout is a staged move of the fleet from From to Target.
//
// While it runs, FleetReleaseState.Tag stays at From, so a backend that does
// not know about rollouts keeps every node where it is. Only admitted nodes
// are told Target.
type FleetRollout struct {
	Target string `json:"target"`
	From   string `json:"from"`
	// Digests maps an image key ("worker", "worker-kafka", "consumer", ...) to
	// the sha256 digest of Target, so an admitted node pulls exactly what was
	// released rather than whatever the tag points at.
	Digests        map[string]string  `json:"digests,omitempty"`
	Policy         FleetRolloutPolicy `json:"policy"`
	Stage          string             `json:"stage"`
	Wave           int                `json:"wave"`
	Admitted       []uuid.UUID        `json:"admitted,omitempty"`
	StartedAt      time.Time          `json:"started_at"`
	StageStartedAt time.Time          `json:"stage_started_at"`
	HaltReason     string             `json:"halt_reason,omitempty"`
}

// Running reports whether admitted nodes are being moved to Target.
func (r *FleetRollout) Running() bool {
	return r != nil && (r.Stage == FleetRolloutCanary || r.Stage == FleetRolloutWave)
}

// IsAdmitted reports whether id has been admitted to the rollout.
func (r *FleetRollout) IsAdmitted(id uuid.UUID) bool {
	if r == nil {
		return false
	}
	for _, a := range r.Admitted {
		if a == id {
			return true
		}
	}
	return false
}
