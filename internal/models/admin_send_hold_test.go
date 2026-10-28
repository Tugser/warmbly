package models

import (
	"testing"

	"github.com/google/uuid"
)

func TestAdminResolveSendHoldResolution(t *testing.T) {
	task := uuid.New()
	for _, tc := range []struct {
		reason, outcome, evidence string
		ok, evidenceTask          bool
	}{
		{"unknown", "sent", "operator_confirmed_sent", true, false},
		{"unknown", "not_sent", "operator_confirmed_not_sent", true, false},
		{"unknown", "provider_confirmed", "", false, false},
		{"authentication", "provider_confirmed", "operator_provider_confirmation", true, true},
		{"permanent", "provider_confirmed", "operator_provider_confirmation", true, true},
		{"conflict", "provider_confirmed", "operator_provider_confirmation", true, true},
		{"permanent", "sent", "", false, false},
		{"conflict", "not_sent", "", false, false},
	} {
		in := AdminResolveSendHold{HeldTaskID: task, HeldReason: tc.reason, Outcome: tc.outcome, MessageID: "<m@x>", ConfirmationReference: "ticket 1"}
		got, ok := in.Resolution()
		if ok != tc.ok {
			t.Fatalf("%s/%s: ok=%v", tc.reason, tc.outcome, ok)
		}
		if !ok {
			continue
		}
		if got.EvidenceType != tc.evidence || got.HeldTaskID != task || got.HeldReason != tc.reason || got.ConfirmationReference != "ticket 1" {
			t.Fatalf("%s/%s: %+v", tc.reason, tc.outcome, got)
		}
		if tc.evidenceTask != (got.EvidenceTaskID != nil && *got.EvidenceTaskID == task) {
			t.Fatalf("%s/%s: evidence task %v", tc.reason, tc.outcome, got.EvidenceTaskID)
		}
		// The not-sent path refuses any message handle, so none may leak into it.
		if tc.outcome != "sent" && (got.MessageID != "" || got.ProviderMsgID != "" || got.ThreadID != "") {
			t.Fatalf("%s/%s carried message handles", tc.reason, tc.outcome)
		}
	}
}
