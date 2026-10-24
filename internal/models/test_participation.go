package models

type TestParticipationMode string

const (
	TestParticipationLegacy     TestParticipationMode = "legacy"
	TestParticipationDiagnostic TestParticipationMode = "diagnostic"
	TestParticipationOff        TestParticipationMode = "off"
)

func (e *Email) LegacyTestParticipation() bool {
	return e.TestMode == nil || *e.TestMode == TestParticipationLegacy
}

func (e *Email) TestSendingAllowed() bool {
	return e.LegacyTestParticipation() || *e.TestMode == TestParticipationDiagnostic && e.TestSendEnabled
}

func (e *Email) TestReceivingAllowed() bool {
	return e.LegacyTestParticipation() || *e.TestMode == TestParticipationDiagnostic && e.TestReceiveEnabled
}

// WarmupReceivingAllowed: new warmup mail reaches only a mailbox whose warmup is on and whose participation receives.
func (e *Email) WarmupReceivingAllowed() bool {
	return e.IsWarmingActive() && e.TestReceivingAllowed()
}

func (e *Email) SyntheticActionsAllowed() bool {
	return e.TestReceivingAllowed()
}

func (e *Email) PermittedWarmupActions(actions []string) []string {
	var out []string
	for _, a := range actions {
		switch a {
		case WarmupActionDelete, WarmupActionVerifyRemoval:
			out = append(out, a)
		case WarmupActionFile:
			if e.TestReceivingAllowed() {
				out = append(out, a)
			}
		case WarmupActionMarkRead, WarmupActionRescueFromSpam, WarmupActionMarkImportant, WarmupActionStar:
			if e.SyntheticActionsAllowed() && e.TestReceivingAllowed() {
				out = append(out, a)
			}
		}
	}
	return out
}
