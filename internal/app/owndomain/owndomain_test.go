package owndomain

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/errx"
)

type fakeMailboxes []string

func (f fakeMailboxes) OwnedDomains(context.Context, uuid.UUID) ([]string, *errx.Error) {
	return f, nil
}

type fakeForms struct {
	domain   string
	verified bool
}

func (f fakeForms) GetFormsDomain(context.Context, uuid.UUID) (string, bool, error) {
	return f.domain, f.verified, nil
}

func TestWithinMatchesOnlyTheDomainAndItsSubdomains(t *testing.T) {
	own := []string{"acme.com"}
	cases := map[string]bool{
		"acme.com":           true,
		"www.acme.com":       true,
		"deep.blog.acme.com": true,
		"ACME.COM.":          true,
		"Www.Acme.Com":       true,
		"evil-acme.com":      false,
		"notacme.com":        false,
		"acme.com.evil.io":   false,
		"acme.co":            false,
		"com":                false,
		"127.0.0.1":          false,
		"[::1]":              false,
		"":                   false,
	}
	for host, want := range cases {
		if got := Within(host, own); got != want {
			t.Errorf("Within(%q) = %v, want %v", host, got, want)
		}
	}
	if Within("10.0.0.1", []string{"10.0.0.1"}) {
		t.Error("an IP literal counts as a domain")
	}
}

func TestWithinComparesInternationalNamesInASCII(t *testing.T) {
	if !Within("www.bücher.de", []string{"xn--bcher-kva.de"}) {
		t.Error("a Unicode host did not match its punycode domain")
	}
	if !Within("shop.xn--bcher-kva.de", []string{"bücher.de"}) {
		t.Error("a punycode host did not match its Unicode domain")
	}
}

func TestTargetHostReadsOnlyAbsoluteWebAddresses(t *testing.T) {
	cases := map[string]string{
		"https://Shop.Acme.com./thanks?x=1": "shop.acme.com",
		"http://acme.com:8080/":             "acme.com",
		"https://user@acme.com/":            "",
		"javascript:alert(1)":               "",
		"//acme.com/x":                      "",
		"acme.com":                          "",
		"https://192.0.2.1/":                "",
	}
	for raw, want := range cases {
		if got := TargetHost(raw); got != want {
			t.Errorf("TargetHost(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestDomainsAreMailboxDomainsAndTheVerifiedFormsDomain(t *testing.T) {
	ctx := context.Background()
	org := uuid.New()

	r := New(fakeMailboxes{"Acme.com", "gmail.com", "outlook.com", "link.acme.io"}, fakeForms{domain: "forms.acme.net", verified: true})
	got, xerr := r.Domains(ctx, org)
	if xerr != nil {
		t.Fatal(xerr)
	}
	want := map[string]bool{"acme.com": true, "link.acme.io": true, "forms.acme.net": true}
	if len(got) != len(want) {
		t.Fatalf("Domains = %v", got)
	}
	for _, d := range got {
		if !want[d] {
			t.Fatalf("Domains = %v, has %q", got, d)
		}
	}

	for target, ok := range map[string]bool{
		"https://acme.com/thanks":   true,
		"https://forms.acme.net/":   true,
		"https://mail.google.com/":  false,
		"https://gmail.com/":        false,
		"https://evil-acme.com/":    false,
		"https://acme.com.evil.io/": false,
		"https://www.acme.net/":     false,
		"https://user@acme.com/":    false,
		"ftp://acme.com/":           false,
		"":                          false,
	} {
		if got, _ := r.Allows(ctx, org, target); got != ok {
			t.Errorf("Allows(%q) = %v, want %v", target, got, ok)
		}
	}

	unverified := New(fakeMailboxes{"acme.com"}, fakeForms{domain: "forms.acme.net"})
	if ok, _ := unverified.Allows(ctx, org, "https://forms.acme.net/"); ok {
		t.Error("an unverified forms domain counts as the workspace's own")
	}
}
