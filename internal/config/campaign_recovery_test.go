package config

import "testing"

func TestCampaignIsolationRequiresExplicitActivation(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{{"", false}, {"false", false}, {"invalid", false}, {"true", true}} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("CAMPAIGN_SEND_ISOLATION_ENABLED", tc.value)
			if got := CampaignSendIsolationEnabled(); got != tc.want {
				t.Fatalf("activation=%v, want %v", got, tc.want)
			}
		})
	}
}
