// Package formwire is the wire contract between the backend's internal form
// endpoints and the forms service (cmd/forms). Both processes marshal these
// structs, so the two sides of the internal API cannot drift apart silently.
package formwire

import "github.com/warmbly/warmbly/internal/models"

// PublicForm is everything the forms service needs to render one published
// form: the definition plus the resolved captcha site key, and nothing the
// visitor must not see (no org id, no counters, no owner).
type PublicForm struct {
	PublicID       string             `json:"public_id"`
	Name           string             `json:"name"`
	Fields         []models.FormField `json:"fields"`
	Design         models.FormDesign  `json:"design"`
	LogoURL        string             `json:"logo_url,omitempty"`
	CoverURL       string             `json:"cover_url,omitempty"`
	BackgroundURL  string             `json:"background_url,omitempty"`
	AllowedDomains []string           `json:"allowed_domains,omitempty"`
	CaptchaSiteKey string             `json:"captcha_site_key,omitempty"`
	// Brand is the "powered by" attribution at the foot of the page. Absent on
	// a self-host that configured none, because a stranger filling in an
	// operator's form has no business being sent to the platform's website.
	Brand *Brand `json:"brand,omitempty"`
	// Prefill and LinkToken appear only when a valid personalized ?t= ticket
	// accompanied the fetch: values for the mapped fields, and the token
	// echoed for submit/event attribution.
	Prefill   map[string]string `json:"prefill,omitempty"`
	LinkToken string            `json:"link_token,omitempty"`
}

// Brand is the deployment's public attribution on a form page.
type Brand struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

// SubmitRequest carries a visitor's answers plus the abuse signals only the
// public-facing process can observe.
type SubmitRequest struct {
	Answers        map[string][]string `json:"answers"`
	RemoteIP       string              `json:"remote_ip"`
	SourceURL      string              `json:"source_url,omitempty"`
	CaptchaToken   string              `json:"captcha_token,omitempty"`
	HoneypotFilled bool                `json:"honeypot_filled,omitempty"`
	// RenderedAt is the unix second the page was served; bots submit near-instantly.
	RenderedAt int64 `json:"rendered_at,omitempty"`
	// LinkToken is the personalized ?t= ticket; VisitorKey ties the
	// submission to the visitor's funnel events.
	LinkToken  string `json:"link_token,omitempty"`
	VisitorKey string `json:"visitor_key,omitempty"`
	// Host is the host the visitor reached the form on; on a workspace's custom host only its own forms answer.
	Host string `json:"host,omitempty"`
}

// EventRequest is one funnel event forwarded by the forms service, plus the
// request attributes the backend enriches from (RemoteIP becomes a country
// and is discarded, UserAgent becomes a device class).
type EventRequest struct {
	Type       string `json:"type"`
	PageIndex  int    `json:"page_index"`
	PagesTotal int    `json:"pages_total"`
	VisitorKey string `json:"visitor_key"`
	SourceURL  string `json:"source_url,omitempty"`
	LinkToken  string `json:"link_token,omitempty"`
	RemoteIP   string `json:"remote_ip"`
	UserAgent  string `json:"user_agent,omitempty"`
	Host       string `json:"host,omitempty"`
}

// SubmitResult is what the visitor sees after a successful submit.
type SubmitResult struct {
	Message     string `json:"message"`
	RedirectURL string `json:"redirect_url,omitempty"`
}

// SubmitError is the 400 body for a rejected submission; Message is
// visitor-facing and shown inline on the form.
type SubmitError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}
