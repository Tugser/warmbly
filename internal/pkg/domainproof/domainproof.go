// Package domainproof derives the DNS TXT value a workspace publishes to prove
// it controls a domain. The value is keyed by the instance secret and bound to
// one workspace and one domain, so it cannot be copied from someone else's
// public DNS or carried to another workspace in an archive.
package domainproof

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/pkg/trackdns"
)

// Label is where the record lives, under the domain.
const Label = "_warmbly"

// Prover derives proof values from the instance secret.
type Prover struct{ key []byte }

// New keys a prover from the instance secret; the secret is never used directly.
func New(secret string) *Prover {
	sum := sha256.Sum256([]byte("warmbly-domain-proof\x00" + secret))
	return &Prover{key: sum[:]}
}

// Name is the TXT record's host name for a domain.
func Name(domain string) string {
	return Label + "." + strings.ToLower(strings.TrimSuffix(domain, "."))
}

// Value is the TXT record's content for one workspace and domain.
func (p *Prover) Value(orgID uuid.UUID, domain string) string {
	mac := hmac.New(sha256.New, p.key)
	mac.Write([]byte(orgID.String() + "|" + strings.ToLower(strings.TrimSuffix(domain, "."))))
	return "warmbly-verify=" + hex.EncodeToString(mac.Sum(nil)[:16])
}

// Matches reports whether any published TXT string is this workspace's proof for the domain.
func (p *Prover) Matches(txts []string, orgID uuid.UUID, domain string) bool {
	want := p.Value(orgID, domain)
	for _, t := range txts {
		if hmac.Equal([]byte(strings.TrimSpace(t)), []byte(want)) {
			return true
		}
	}
	return false
}

// CodeProofMissing is a record that points here without the workspace's TXT proof beside it.
const CodeProofMissing = "proof_missing"

// TXTResolver is the one lookup a proof check makes; *net.Resolver satisfies it.
type TXTResolver interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
}

const lookupTimeout = 5 * time.Second

// Record is the TXT name and value to publish, empty when there is no domain or no prover.
func (p *Prover) Record(orgID uuid.UUID, domain string) (name, value string) {
	if p == nil || domain == "" {
		return "", ""
	}
	return Name(domain), p.Value(orgID, domain)
}

// Published reports whether the workspace's proof is in DNS at the domain or at a parent it sits under; an error means a lookup could not tell.
func (p *Prover) Published(ctx context.Context, r TXTResolver, orgID uuid.UUID, domain string) (bool, error) {
	if p == nil {
		return false, nil
	}
	if r == nil {
		r = net.DefaultResolver
	}
	cctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	var lookupErr error
	for _, d := range proofDomains(domain) {
		txts, err := r.LookupTXT(cctx, Name(d))
		if p.Matches(txts, orgID, d) {
			return true, nil
		}
		var dnsErr *net.DNSError
		if err != nil && !(errors.As(err, &dnsErr) && dnsErr.IsNotFound) && lookupErr == nil {
			lookupErr = err
		}
	}
	return false, lookupErr
}

// proofDomains is the domain and each parent of at least two labels, most specific first.
func proofDomains(domain string) []string {
	d := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	if d == "" {
		return nil
	}
	out := []string{d}
	for strings.Count(d, ".") > 1 {
		d = d[strings.Index(d, ".")+1:]
		out = append(out, d)
	}
	return out
}

// Gate holds a record that points here to the workspace's TXT proof, unless held says the workspace already verified the host.
func (p *Prover) Gate(ctx context.Context, r TXTResolver, res trackdns.Result, orgID uuid.UUID, held bool) trackdns.Result {
	if !res.Verified || held {
		return res
	}
	ok, err := p.Published(ctx, r, orgID, res.Domain)
	if ok {
		return res
	}
	res.Verified, res.Method = false, ""
	if err != nil {
		res.Code = trackdns.CodeLookupError
		res.Reason = fmt.Sprintf("%s points here, but the TXT record %s could not be looked up right now. Try again in a minute.", res.Domain, Name(res.Domain))
		return res
	}
	res.Code = CodeProofMissing
	res.Reason = fmt.Sprintf("%s points here. Add the TXT record %s with this workspace's value to prove the domain is yours. DNS changes can take a while to appear.", res.Domain, Name(res.Domain))
	return res
}
