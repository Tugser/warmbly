package repository

import (
	"slices"
	"time"

	"github.com/warmbly/warmbly/internal/models"
)

// cloudCommandAdmission is what admission reads about a Cloud-linked mailbox.
type cloudCommandAdmission struct {
	// Linked is false for a mailbox no cloud link enrolls.
	Linked   bool
	Executor models.PoolLinkExecutor
	// AdmitUntil and Actions describe the one command the work carries, when it names a live one.
	AdmitUntil *time.Time
	Actions    []string
	Kind       string
	State      string
}

// cloudActionsAdmitted filters warmup actions on a Cloud-linked mailbox. Local
// warmup stands down for every enrolled mailbox; the one exception is an
// instance-executed mailbox acting under the unexpired Cloud command it carries.
func cloudActionsAdmitted(a cloudCommandAdmission, requested []string, now time.Time) []string {
	if !a.Linked {
		return requested
	}
	if a.Executor.OrCloud() != models.PoolLinkExecutorInstance || a.AdmitUntil == nil || !now.Before(*a.AdmitUntil) {
		return nil
	}
	var out []string
	for _, act := range requested {
		if slices.Contains(a.Actions, act) {
			out = append(out, act)
		}
	}
	return out
}

// cloudSendAdmitted reports whether a cloud_warmup send may run: its command is
// an unanswered, unexpired send of an instance-executed mailbox.
func cloudSendAdmitted(a cloudCommandAdmission, now time.Time) bool {
	return a.Linked && a.Executor.OrCloud() == models.PoolLinkExecutorInstance && a.Kind == models.PoolLinkCommandSend &&
		(a.State == "pending" || a.State == "dispatched") && a.AdmitUntil != nil && now.Before(*a.AdmitUntil)
}
