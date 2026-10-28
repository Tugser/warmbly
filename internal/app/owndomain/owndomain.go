// Package owndomain decides which hosts belong to a workspace, so a redirect
// the workspace configures (a form's after-submit page, a sending domain's
// root) only ever sends a visitor to the workspace's own site.
package owndomain

import (
	"context"
	"net"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/pkg/mailhost"
)

// ErrIDNotOwn refuses a redirect destination outside the workspace's own domains.
const ErrIDNotOwn = "redirect_target_not_own_domain"

// Refusal is the save-time error for a destination outside the workspace's own domains.
func Refusal() *errx.Error {
	return errx.NewWithIdentifier(errx.BadRequest, ErrIDNotOwn, RefusalMessage)
}

// RefusalMessage names the kinds of domain a redirect may point at.
const RefusalMessage = "The redirect has to go to a page on one of this workspace's own domains: a domain it has a mailbox on (not a shared provider such as gmail.com), its verified tracking domain, its verified forms domain, or a subdomain of one of them. Use an address on one of those, or connect a mailbox or verify a domain first."

// Mailboxes lists the domains the workspace has a mailbox on and its verified tracking hosts.
type Mailboxes interface {
	OwnedDomains(ctx context.Context, orgID uuid.UUID) ([]string, *errx.Error)
}

// FormsDomains is the workspace's custom forms domain and whether it verified.
type FormsDomains interface {
	GetFormsDomain(ctx context.Context, orgID uuid.UUID) (string, bool, error)
}

// Resolver is the one definition of a workspace's own domains, read at save time and at serve time.
type Resolver struct {
	mailboxes Mailboxes
	forms     FormsDomains
}

// New builds the resolver; forms may be nil.
func New(mailboxes Mailboxes, forms FormsDomains) *Resolver {
	return &Resolver{mailboxes: mailboxes, forms: forms}
}

// Domains are the workspace's own domains: every domain it has a mailbox on
// except shared providers, its verified tracking hosts, and its custom forms
// domain once verified. Each is normalized; a host under one is also the workspace's.
func (r *Resolver) Domains(ctx context.Context, orgID uuid.UUID) ([]string, *errx.Error) {
	var raw []string
	if r.mailboxes != nil {
		list, xerr := r.mailboxes.OwnedDomains(ctx, orgID)
		if xerr != nil {
			return nil, xerr
		}
		raw = append(raw, list...)
	}
	if r.forms != nil {
		domain, verified, err := r.forms.GetFormsDomain(ctx, orgID)
		if err != nil {
			return nil, errx.InternalError()
		}
		if verified && domain != "" {
			raw = append(raw, domain)
		}
	}
	out := make([]string, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for _, d := range raw {
		d = Normalize(d)
		if d == "" || seen[d] || mailhost.SharedProvider(d) {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out, nil
}

// Allows reports whether the destination URL's host is one of the workspace's own domains or under one.
func (r *Resolver) Allows(ctx context.Context, orgID uuid.UUID, target string) (bool, *errx.Error) {
	host := TargetHost(target)
	if host == "" {
		return false, nil
	}
	domains, xerr := r.Domains(ctx, orgID)
	if xerr != nil {
		return false, xerr
	}
	return Within(host, domains), nil
}

// TargetHost is the normalized host of an absolute http(s) URL, or "" when it has none.
func TargetHost(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return ""
	}
	return Normalize(u.Hostname())
}

// Normalize lowercases a hostname, drops a trailing dot and converts it to
// its ASCII (punycode) form; an IP literal or anything that is not a
// hostname normalizes to "".
func Normalize(host string) string {
	host = strings.TrimSuffix(strings.TrimSpace(host), ".")
	if host == "" || net.ParseIP(strings.Trim(host, "[]")) != nil {
		return ""
	}
	return mailhost.NormalizeDomain(host)
}

// Within reports whether host equals one of domains or is a subdomain of one, compared label by label.
func Within(host string, domains []string) bool {
	host = Normalize(host)
	if host == "" {
		return false
	}
	for _, d := range domains {
		d = Normalize(d)
		if d == "" {
			continue
		}
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}
