package domainproof

import (
	"context"
	"net"
	"testing"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/pkg/trackdns"
)

type txtMap map[string][]string

func (m txtMap) LookupTXT(_ context.Context, name string) ([]string, error) {
	if v, ok := m[name]; ok {
		return v, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}

type txtDown struct{}

func (txtDown) LookupTXT(context.Context, string) ([]string, error) {
	return nil, &net.DNSError{Err: "server misbehaving", IsTemporary: true}
}

func TestGateHoldsANewVerificationToTheProof(t *testing.T) {
	ctx := context.Background()
	org := uuid.New()
	p := New("instance-secret")
	cname := trackdns.Result{Domain: "link.acme.io", Verified: true, Code: trackdns.CodeVerified, Method: trackdns.MethodCNAME}

	if got := p.Gate(ctx, txtMap{}, cname, org, false); got.Verified || got.Code != CodeProofMissing {
		t.Fatalf("verified without proof: %+v", got)
	}
	if got := p.Gate(ctx, txtMap{}, cname, org, true); !got.Verified {
		t.Fatal("a held domain lost its verdict")
	}
	if got := p.Gate(ctx, txtMap{"_warmbly.link.acme.io": {p.Value(org, "link.acme.io")}}, cname, org, false); !got.Verified {
		t.Fatal("proof on the host was not accepted")
	}
	if got := p.Gate(ctx, txtMap{"_warmbly.acme.io": {p.Value(org, "acme.io")}}, cname, org, false); !got.Verified {
		t.Fatal("proof on the parent domain was not accepted")
	}
	if got := p.Gate(ctx, txtMap{"_warmbly.link.acme.io": {p.Value(uuid.New(), "link.acme.io")}}, cname, org, false); got.Verified {
		t.Fatal("another workspace's proof was accepted")
	}
	if got := p.Gate(ctx, txtDown{}, cname, org, false); got.Verified || got.Code != trackdns.CodeLookupError {
		t.Fatalf("a failed lookup was not reported as one: %+v", got)
	}
	var none *Prover
	if got := none.Gate(ctx, txtMap{}, cname, org, false); got.Verified {
		t.Fatal("a missing prover verified a new domain")
	}
	if got := proofDomains("a.b.acme.io."); len(got) != 3 || got[0] != "a.b.acme.io" || got[2] != "acme.io" {
		t.Fatalf("proofDomains: %v", got)
	}
}
