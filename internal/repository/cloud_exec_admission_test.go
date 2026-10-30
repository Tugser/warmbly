package repository

import (
	"slices"
	"testing"
	"time"

	"github.com/warmbly/warmbly/internal/models"
)

func TestCloudActionsAdmitted(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	live, lapsed := now.Add(time.Minute), now.Add(-time.Second)
	requested := []string{models.WarmupActionFile, models.WarmupActionMarkRead}
	cases := []struct {
		name string
		in   cloudCommandAdmission
		want []string
	}{
		{"unlinked mailbox keeps its own warmup", cloudCommandAdmission{}, requested},
		{"cloud executor stands down", cloudCommandAdmission{Linked: true, Executor: models.PoolLinkExecutorCloud, AdmitUntil: &live, Actions: requested}, nil},
		{"legacy row reads as cloud", cloudCommandAdmission{Linked: true, AdmitUntil: &live, Actions: requested}, nil},
		{"instance without a command", cloudCommandAdmission{Linked: true, Executor: models.PoolLinkExecutorInstance}, nil},
		{"instance with a lapsed command", cloudCommandAdmission{Linked: true, Executor: models.PoolLinkExecutorInstance, AdmitUntil: &lapsed, Actions: requested}, nil},
		{"instance with a live command", cloudCommandAdmission{Linked: true, Executor: models.PoolLinkExecutorInstance, AdmitUntil: &live, Actions: requested}, requested},
		{"only the commanded actions", cloudCommandAdmission{Linked: true, Executor: models.PoolLinkExecutorInstance, AdmitUntil: &live,
			Actions: []string{models.WarmupActionMarkRead}}, []string{models.WarmupActionMarkRead}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cloudActionsAdmitted(tc.in, requested, now); !slices.Equal(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCloudSendAdmitted(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	live, lapsed := now.Add(time.Minute), now
	ok := cloudCommandAdmission{Linked: true, Executor: models.PoolLinkExecutorInstance, AdmitUntil: &live, Kind: models.PoolLinkCommandSend, State: "dispatched"}
	if !cloudSendAdmitted(ok, now) {
		t.Fatal("a live send command on an instance-executed mailbox is admitted")
	}
	pending := ok
	pending.State = "pending"
	if !cloudSendAdmitted(pending, now) {
		t.Fatal("the worker may begin before the pump marks the command dispatched")
	}
	for name, mut := range map[string]func(*cloudCommandAdmission){
		"unlinked":       func(a *cloudCommandAdmission) { a.Linked = false },
		"cloud executor": func(a *cloudCommandAdmission) { a.Executor = models.PoolLinkExecutorCloud },
		"answered":       func(a *cloudCommandAdmission) { a.State = "answered" },
		"expired":        func(a *cloudCommandAdmission) { a.AdmitUntil = &lapsed },
		"no deadline":    func(a *cloudCommandAdmission) { a.AdmitUntil = nil },
		"action command": func(a *cloudCommandAdmission) { a.Kind = models.PoolLinkCommandAction },
	} {
		a := ok
		mut(&a)
		if cloudSendAdmitted(a, now) {
			t.Fatalf("%s must not be admitted", name)
		}
	}
}
