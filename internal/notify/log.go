package notify

import (
	"context"
	"log"
	"regexp"
	"strings"

	"github.com/warmbly/warmbly/internal/config"
)

// logEmailNotificationService writes messages to stdout instead of delivering
// them, so a fresh install works before any relay is configured. A transport
// that does not deliver never gates a login or a signup on an emailed code, and
// outside development every link's query and fragment is withheld.
//
// The banner is deliberately loud because this transport must never be mistaken
// for working mail.
type logEmailNotificationService struct {
	Name    string
	Address string
}

func NewLogEmailNotificationService(name, address string) EmailNotificationService {
	return &logEmailNotificationService{Name: name, Address: address}
}

func (s *logEmailNotificationService) Send(ctx context.Context, to, cc, bcc []string, subject, message string) error {
	return s.write(to, cc, bcc, "", subject, message)
}

func (s *logEmailNotificationService) SendOutreach(ctx context.Context, to []string, replyTo, subject, message string) error {
	return s.write(to, nil, nil, replyTo, subject, message)
}

func (s *logEmailNotificationService) write(to, cc, bcc []string, replyTo, subject, html string) error {
	recipients := append(append(append([]string{}, to...), cc...), bcc...)

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString("┌──────────────────────────────────────────────────────────────────────\n")
	b.WriteString("│ MAIL_TRANSPORT=log — this message was NOT delivered\n")
	b.WriteString("│ To:      " + strings.Join(recipients, ", ") + "\n")
	b.WriteString("│ From:    " + formatAddress(s.Name, s.Address) + "\n")
	if replyTo != "" {
		b.WriteString("│ Reply-To: " + replyTo + "\n")
	}
	b.WriteString("│ Subject: " + subject + "\n")
	body := htmlToPlainText(html)
	if !config.ExplicitDevelopment() {
		body = redactLinkParams(body)
		b.WriteString("│ Links:   query strings and fragments removed. Copy invitation links under Settings > Members; reset passwords with warmblyctl user reset-password\n")
	}
	b.WriteString("├──────────────────────────────────────────────────────────────────────\n")
	for _, line := range strings.Split(body, "\n") {
		b.WriteString("│ " + line + "\n")
	}
	b.WriteString("└──────────────────────────────────────────────────────────────────────\n")

	log.Print(b.String())
	return nil
}

var logLink = regexp.MustCompile(`https?://[^\s<>"']+`)

// redactLinkParams drops the query and fragment of every link, where reset and invitation tokens travel.
func redactLinkParams(s string) string {
	return logLink.ReplaceAllStringFunc(s, func(raw string) string {
		cut := strings.IndexAny(raw, "?#")
		if cut < 0 {
			return raw
		}
		return raw[:cut] + "?[redacted]"
	})
}
