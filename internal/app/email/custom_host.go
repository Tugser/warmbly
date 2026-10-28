package email

import (
	"context"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/customhost"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/pkg/trackdns"
)

// WireCustomHosts attaches the custom-domain ownership checks a new tracking domain verification goes through.
func (s *emailService) WireCustomHosts(c *customhost.Checker) { s.customHosts = c }

// CustomHostsAware is the optional capability main uses to attach the checker.
type CustomHostsAware interface {
	WireCustomHosts(c *customhost.Checker)
}

// gateTrackingDomain holds a CNAME verdict to the workspace's TXT proof unless it already holds the host.
func (s *emailService) gateTrackingDomain(ctx context.Context, orgID string, res trackdns.Result, held bool) trackdns.Result {
	org, err := uuid.Parse(orgID)
	if err != nil {
		res.Verified = false
		return res
	}
	return s.customHosts.Gate(ctx, res, org, held)
}

// withProofRecord fills the TXT record the status shows beside the CNAME.
func (s *emailService) withProofRecord(status *models.TrackingDomainStatus, orgID string) *models.TrackingDomainStatus {
	org, err := uuid.Parse(orgID)
	if err != nil || status == nil || status.TrackingDomain == "" {
		return status
	}
	status.TXTName, status.TXTValue = s.customHosts.Record(org, status.TrackingDomain)
	return status
}
