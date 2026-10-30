package config

import (
	"os"
	"strconv"
	"strings"
)

// CampaignSendIsolationEnabled stays off until recovery-aware replicas form the rollback floor.
func CampaignSendIsolationEnabled() bool {
	enabled, err := strconv.ParseBool(strings.TrimSpace(os.Getenv("CAMPAIGN_SEND_ISOLATION_ENABLED")))
	return err == nil && enabled
}
