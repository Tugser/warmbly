package models

import (
	"slices"
	"testing"
	"time"
)

func TestWarmupEngagementRequiresReceivingParticipation(t *testing.T) {
	actions := []string{WarmupActionFile, WarmupActionRescueFromSpam, WarmupActionMarkRead, WarmupActionMarkImportant, WarmupActionStar, WarmupActionDelete, WarmupActionVerifyRemoval, "unknown"}
	cleanup := []string{WarmupActionDelete, WarmupActionVerifyRemoval}
	for _, tc := range []struct {
		name    string
		mode    TestParticipationMode
		send    bool
		receive bool
		allowed bool
	}{
		{"legacy NULL", "", false, false, true},
		{"explicit legacy", TestParticipationLegacy, false, false, true},
		{"send and receive", TestParticipationDiagnostic, true, true, true},
		{"receive only", TestParticipationDiagnostic, false, true, true},
		{"send only", TestParticipationDiagnostic, true, false, false},
		{"neither direction", TestParticipationDiagnostic, false, false, false},
		{"off with old flags", TestParticipationOff, true, true, false},
		{"unknown mode", "unknown", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := Email{TestSendEnabled: tc.send, TestReceiveEnabled: tc.receive}
			if tc.mode != "" {
				e.TestMode = &tc.mode
			}
			want := cleanup
			if tc.allowed {
				want = actions[:len(actions)-1]
			}
			if got := e.PermittedWarmupActions(actions); !slices.Equal(got, want) || e.SyntheticActionsAllowed() != tc.allowed {
				t.Fatalf("actions=%v want=%v engagement=%v", got, want, e.SyntheticActionsAllowed())
			}
		})
	}
}

func TestWarmupReceivingRequiresWarmupOn(t *testing.T) {
	started, paused := time.Now().Add(-48*time.Hour), time.Now().Add(-time.Hour)
	diagnostic, off, legacy := TestParticipationDiagnostic, TestParticipationOff, TestParticipationLegacy
	for _, tc := range []struct {
		name            string
		warmup, pausedA *time.Time
		mode            *TestParticipationMode
		send, receive   bool
		want            bool
	}{
		{"legacy, warmup on", &started, nil, nil, false, false, true},
		{"explicit legacy, warmup on", &started, nil, &legacy, false, false, true},
		{"legacy, never started", nil, nil, nil, false, false, false},
		{"legacy, paused", &started, &paused, nil, false, false, false},
		{"paused as the pause action leaves it", &started, &paused, &diagnostic, false, true, false},
		{"explicit receive-only, warmup on", &started, nil, &diagnostic, false, true, true},
		{"send and receive, warmup on", &started, nil, &diagnostic, true, true, true},
		{"send only, warmup on", &started, nil, &diagnostic, true, false, false},
		{"off with old flags, warmup on", &started, nil, &off, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := Email{Warmup: tc.warmup, WarmupPausedAt: tc.pausedA, TestMode: tc.mode, TestSendEnabled: tc.send, TestReceiveEnabled: tc.receive}
			if got := e.WarmupReceivingAllowed(); got != tc.want {
				t.Fatalf("WarmupReceivingAllowed()=%v want %v", got, tc.want)
			}
		})
	}
}
