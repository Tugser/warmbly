package mailhost

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// clientConfig is the Mozilla autoconfig / Thunderbird ISPDB document.
type clientConfig struct {
	XMLName  xml.Name `xml:"clientConfig"`
	Provider struct {
		Incoming []serverConfig `xml:"incomingServer"`
		Outgoing []serverConfig `xml:"outgoingServer"`
	} `xml:"emailProvider"`
}

type serverConfig struct {
	Type       string   `xml:"type,attr"`
	Hostname   string   `xml:"hostname"`
	Port       string   `xml:"port"`
	SocketType string   `xml:"socketType"`
	Auth       []string `xml:"authentication"`
}

// autoconfig tries the provider-hosted autoconfig files for domain.
func (d *Detector) autoconfig(ctx context.Context, domain string) *Settings {
	for _, u := range d.autoconfigURLs(domain) {
		if ctx.Err() != nil {
			return nil
		}
		if s := d.fetchConfig(ctx, u, domain); s != nil {
			return s
		}
	}
	return nil
}

// ispdb asks Thunderbird's ISPDB about lookup, substituting domain into the answer.
func (d *Detector) ispdb(ctx context.Context, lookup, domain string) *Settings {
	if ctx.Err() != nil || d.ispdbBase == "" {
		return nil
	}
	return d.fetchConfig(ctx, d.ispdbBase+url.PathEscape(lookup), domain)
}

// fetchConfig downloads and parses one clientConfig; any failure is a miss.
func (d *Detector) fetchConfig(ctx context.Context, u, domain string) *Settings {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Accept", "application/xml, text/xml")
	resp, err := d.client.Do(req)
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil || len(body) > maxBody {
		return nil
	}
	return parseClientConfig(body, domain)
}

// parseClientConfig picks one IMAP and one SMTP server from a clientConfig
// document, or nil when it does not offer both over TLS.
func parseClientConfig(body []byte, domain string) *Settings {
	var cfg clientConfig
	if err := decodeUntrustedXML(body, &cfg); err != nil {
		return nil
	}
	imap, okI := pick(cfg.Provider.Incoming, "imap", domain, imapRank)
	smtp, okS := pick(cfg.Provider.Outgoing, "smtp", domain, smtpRank)
	if !okI || !okS {
		return nil
	}
	return &Settings{SMTP: smtp, IMAP: imap}
}

var errXMLRefused = errors.New("xml document refused")

// decodeUntrustedXML decodes a bounded, well-formed document that carries no DTD or other directive.
func decodeUntrustedXML(body []byte, v any) error {
	if len(body) > maxBody {
		return errXMLRefused
	}
	scan := xml.NewDecoder(bytes.NewReader(body))
	scan.Strict = true
	for {
		tok, err := scan.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if _, ok := tok.(xml.Directive); ok {
			return errXMLRefused
		}
	}
	dec := xml.NewDecoder(bytes.NewReader(body))
	dec.Strict = true
	return dec.Decode(v)
}

// imapRank prefers implicit TLS on 993.
func imapRank(e Endpoint) int {
	switch {
	case e.Security == SecurityTLS && e.Port == 993:
		return 0
	case e.Security == SecurityTLS:
		return 1
	}
	return 2
}

// smtpRank prefers 587 with STARTTLS, then 465 with implicit TLS.
func smtpRank(e Endpoint) int {
	switch {
	case e.Security == SecurityStartTLS && e.Port == 587:
		return 0
	case e.Security == SecurityTLS && e.Port == 465:
		return 1
	case e.Security == SecurityTLS:
		return 2
	}
	return 3
}

func pick(servers []serverConfig, kind, domain string, rank func(Endpoint) int) (Endpoint, bool) {
	var best Endpoint
	bestRank := -1
	for _, s := range servers {
		if !strings.EqualFold(strings.TrimSpace(s.Type), kind) || !passwordAuth(s.Auth) {
			continue
		}
		e, ok := endpoint(s, kind, domain)
		if !ok {
			continue
		}
		if r := rank(e); bestRank < 0 || r < bestRank {
			best, bestRank = e, r
		}
	}
	return best, bestRank >= 0
}

// passwordAuth reports whether a server takes a password, true when it does not say.
func passwordAuth(methods []string) bool {
	if len(methods) == 0 {
		return true
	}
	for _, m := range methods {
		switch strings.ToLower(strings.TrimSpace(m)) {
		case "password-cleartext", "password-encrypted", "plain", "secure":
			return true
		}
	}
	return false
}

func endpoint(s serverConfig, kind, domain string) (Endpoint, bool) {
	host := strings.ReplaceAll(strings.TrimSpace(s.Hostname), "%EMAILDOMAIN%", domain)
	host = normHost(host)
	if host == "" || !validHostname(host) || !strings.Contains(host, ".") {
		return Endpoint{}, false
	}
	var sec string
	switch strings.ToUpper(strings.TrimSpace(s.SocketType)) {
	case "SSL", "TLS":
		sec = SecurityTLS
	case "STARTTLS":
		sec = SecurityStartTLS
	default:
		return Endpoint{}, false
	}
	port, err := strconv.Atoi(strings.TrimSpace(s.Port))
	if err != nil || port <= 0 || port > 65535 {
		port = defaultPort(kind, sec)
	}
	return Endpoint{Host: host, Port: port, Security: sec}, true
}

func defaultPort(kind, sec string) int {
	switch {
	case kind == "imap" && sec == SecurityTLS:
		return 993
	case kind == "imap":
		return 143
	case sec == SecurityTLS:
		return 465
	}
	return 587
}

// srvSpec is one RFC 6186 / RFC 8314 service record to try.
type srvSpec struct {
	service  string
	security string
}

var (
	srvSMTP = []srvSpec{{"submission", SecurityStartTLS}, {"submissions", SecurityTLS}}
	srvIMAP = []srvSpec{{"imaps", SecurityTLS}, {"imap", SecurityStartTLS}}
)

// srv reads the RFC 6186 service records. err is set when a lookup failed for
// a reason other than the record not existing.
func (d *Detector) srv(ctx context.Context, domain string) (*Settings, error) {
	smtp, errS := d.firstSRV(ctx, domain, srvSMTP)
	if errS != nil && smtp == nil {
		return nil, errS
	}
	if smtp == nil {
		return nil, nil
	}
	imap, errI := d.firstSRV(ctx, domain, srvIMAP)
	if imap == nil {
		return nil, errI
	}
	return &Settings{SMTP: *smtp, IMAP: *imap}, nil
}

func (d *Detector) firstSRV(ctx context.Context, domain string, specs []srvSpec) (*Endpoint, error) {
	var lastErr error
	for _, sp := range specs {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		e, err := d.lookupSRV(ctx, sp, domain)
		if e != nil {
			return e, nil
		}
		if err != nil && !notFound(err) {
			lastErr = err
		}
	}
	return nil, lastErr
}

func (d *Detector) lookupSRV(ctx context.Context, sp srvSpec, domain string) (*Endpoint, error) {
	ctx, cancel := context.WithTimeout(ctx, srvTimeout)
	defer cancel()
	_, recs, err := d.resolver.LookupSRV(ctx, sp.service, "tcp", domain)
	// Records come back sorted by priority and weight.
	for _, r := range recs {
		if r == nil {
			continue
		}
		host := normHost(r.Target)
		// A target of "." says the service is not offered (RFC 2782).
		if host == "" {
			return nil, nil
		}
		if !validHostname(host) || r.Port == 0 {
			continue
		}
		return &Endpoint{Host: host, Port: int(r.Port), Security: sp.security}, nil
	}
	if err == nil {
		return nil, nil
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		return nil, nil
	}
	return nil, err
}
