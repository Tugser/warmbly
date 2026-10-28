package form

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/customhost"
	"github.com/warmbly/warmbly/internal/config"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/pkg/trackdns"
	"github.com/warmbly/warmbly/internal/repository"
)

// OrgStore is the slice of the organization repository the form service
// needs: the custom forms domain, and the workspace owner used as the
// fallback actor when a form outlives the member who created it. Kept narrow
// so the service does not depend on the whole organization repository.
type OrgStore interface {
	GetByID(ctx context.Context, id uuid.UUID) (*models.Organization, error)
	GetFormsDomain(ctx context.Context, orgID uuid.UUID) (string, bool, error)
	SetFormsDomain(ctx context.Context, orgID uuid.UUID, domain string) error
	SetFormsDomainVerified(ctx context.Context, orgID uuid.UUID, verified bool, at *time.Time) error
	FormsDomainHeldElsewhere(ctx context.Context, orgID uuid.UUID, domain string) (bool, error)
}

// FormsHost returns the host every form URL for this organization is built
// on: their own domain once it verifies, otherwise this install's shared
// forms host.
//
// Only a verified domain is honored, for the same reason the tracking host
// works that way: a name that does not resolve turns every form link already
// sitting in a recipient's inbox into a dead link, which is worse than a link
// on the shared host.
func (s *service) FormsHost(ctx context.Context, orgID uuid.UUID) string {
	shared := config.FormsURLHost()
	if s.domains == nil {
		return shared
	}
	domain, verified, err := s.domains.GetFormsDomain(ctx, orgID)
	if err != nil || !verified {
		return shared
	}
	if host := config.NormalizeTrackingHost(domain); host != "" {
		return host
	}
	return shared
}

// FormsDomainStatus reports the stored state without doing DNS work, for the
// settings screen's first paint.
func (s *service) FormsDomainStatus(ctx context.Context, orgID uuid.UUID) (*models.FormsDomainStatus, *errx.Error) {
	if s.domains == nil {
		return nil, errx.InternalError()
	}
	domain, verified, err := s.domains.GetFormsDomain(ctx, orgID)
	if err != nil {
		return nil, errx.InternalError()
	}
	out := &models.FormsDomainStatus{
		FormsDomain:         domain,
		FormsDomainVerified: verified,
		CNAMETarget:         config.FormsHostname(),
	}
	switch {
	case domain == "":
		out.Status = trackdns.CodeUnset
		out.Message = "No custom forms domain is set; form links use the shared host."
	case out.CNAMETarget == "":
		out.Status = trackdns.CodeNoTarget
		out.Message = "This install has no forms host configured, so there is nothing to point a record at."
	case verified:
		out.Status = trackdns.CodeVerified
		out.Message = "Verified. Form links are built on this domain."
	default:
		out.Status = "pending"
		out.Message = "Not verified yet; form links use the shared host until it resolves."
	}
	if domain != "" {
		out.TXTName, out.TXTValue = s.customHosts.Record(orgID, domain)
	}
	return out, nil
}

// verifyCNAME is the DNS half of a forms domain check; a variable so tests run without DNS.
var verifyCNAME = trackdns.Verify

// SetFormsDomain stores a new custom forms domain and immediately resolves
// it, so saving and verifying are one action for the customer. An empty value
// clears it back to the shared host.
func (s *service) SetFormsDomain(ctx context.Context, orgID uuid.UUID, raw string) (*models.FormsDomainStatus, *errx.Error) {
	if s.domains == nil {
		return nil, errx.InternalError()
	}
	domain := config.NormalizeTrackingHost(raw)
	if domain != "" && (!validFormsDomain(domain) || customhost.Platform(domain)) {
		return nil, errx.New(errx.BadRequest, "that does not look like a domain name")
	}
	if held, err := s.domains.FormsDomainHeldElsewhere(ctx, orgID, domain); err != nil {
		return nil, errx.InternalError()
	} else if held {
		return nil, errx.ErrFormsDomainTaken
	}
	if err := s.domains.SetFormsDomain(ctx, orgID, domain); err != nil {
		return nil, errx.InternalError()
	}
	if domain == "" {
		return s.FormsDomainStatus(ctx, orgID)
	}
	return s.VerifyFormsDomain(ctx, orgID)
}

// VerifyFormsDomain re-resolves the stored domain and records the verdict.
// An unresolved record is not an error: it stays unverified with a reason the
// customer can act on, and form links keep working on the shared host. A new
// verification needs the workspace's TXT proof beside the CNAME.
func (s *service) VerifyFormsDomain(ctx context.Context, orgID uuid.UUID) (*models.FormsDomainStatus, *errx.Error) {
	if s.domains == nil {
		return nil, errx.InternalError()
	}
	domain, verified, err := s.domains.GetFormsDomain(ctx, orgID)
	if err != nil {
		return nil, errx.InternalError()
	}

	target := config.FormsHostname()
	res := s.customHosts.Gate(ctx, verifyCNAME(ctx, domain, target), orgID, verified)
	if res.Verified && !verified {
		if held, herr := s.domains.FormsDomainHeldElsewhere(ctx, orgID, res.Domain); herr != nil {
			return nil, errx.InternalError()
		} else if held {
			return nil, errx.ErrFormsDomainTaken
		}
	}

	out := &models.FormsDomainStatus{
		FormsDomain:           res.Domain,
		FormsDomainVerified:   res.Verified,
		CNAMETarget:           target,
		Status:                res.Code,
		Message:               res.Reason,
		Observed:              res.Observed,
		FormsHostUnresolvable: res.TargetUnresolvable,
	}
	if res.Domain != "" {
		out.TXTName, out.TXTValue = s.customHosts.Record(orgID, res.Domain)
	}
	if res.Verified {
		now := time.Now().UTC()
		out.FormsDomainVerifiedAt = &now
	}
	if err := s.domains.SetFormsDomainVerified(ctx, orgID, res.Verified, out.FormsDomainVerifiedAt); err != nil {
		if errors.Is(err, repository.ErrFormsDomainTaken) {
			return nil, errx.ErrFormsDomainTaken
		}
		return nil, errx.InternalError()
	}
	return out, nil
}

// validFormsDomain rejects the obvious non-domains before a DNS lookup: a
// bare label, an IP-looking value, or anything with characters a hostname
// cannot carry.
func validFormsDomain(host string) bool {
	if len(host) > 253 || !strings.Contains(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		for i, r := range label {
			isAlnum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
			if isAlnum {
				continue
			}
			// A hyphen is legal inside a label, never at either end.
			if r == '-' && i > 0 && i < len(label)-1 {
				continue
			}
			return false
		}
	}
	return true
}
