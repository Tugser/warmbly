package repository

import (
	"testing"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
)

func TestSendHoldBlocker(t *testing.T) {
	task := uuid.New()
	hold := func(reason, mailbox, taskStatus, result string, other int) *models.AdminSendHold {
		return &models.AdminSendHold{Reason: reason, MailboxStatus: mailbox, HeldTaskID: &task, TaskStatus: taskStatus, ResultState: result, OtherUnknownSends: other}
	}
	for name, tc := range map[string]struct {
		h                                       *models.AdminSendHold
		applied, pending, settled, sameProvider bool
		want                                    string
	}{
		"unknown resolvable":      {hold("unknown", "active", "completed", "unknown", 0), false, false, true, true, ""},
		"switched off":            {hold("unknown", "inactive", "completed", "unknown", 0), false, false, true, true, models.SendHoldBlockerMailboxInactive},
		"verdict queued":          {hold("unknown", "active", "completed", "unknown", 0), false, true, true, true, models.SendHoldBlockerResolutionPending},
		"result arrived":          {hold("unknown", "active", "completed", "sent", 0), true, false, true, true, models.SendHoldBlockerResultSettling},
		"task still running":      {hold("unknown", "active", "active", "unknown", 0), false, false, true, true, models.SendHoldBlockerTaskNotSettled},
		"too recent":              {hold("unknown", "active", "failed", "unknown", 0), false, false, false, true, models.SendHoldBlockerTooRecent},
		"permanent resolvable":    {hold("permanent", "active", "failed", "failed", 0), true, false, true, true, ""},
		"permanent not applied":   {hold("permanent", "active", "failed", "failed", 0), false, false, true, true, models.SendHoldBlockerResultSettling},
		"unknown sends behind it": {hold("authentication", "active", "failed", "failed", 2), true, false, true, true, models.SendHoldBlockerOtherUnknownSends},
		"provider changed":        {hold("conflict", "active", "completed", "sent", 0), true, false, true, false, models.SendHoldBlockerProviderChanged},
		"no task":                 {&models.AdminSendHold{Reason: "permanent", MailboxStatus: "active"}, true, false, true, true, models.SendHoldBlockerTaskMissing},
	} {
		if got := sendHoldBlocker(tc.h, tc.applied, tc.pending, tc.settled, tc.sameProvider); got != tc.want {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}
