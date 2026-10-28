package models

import (
	"testing"

	"github.com/warmbly/warmbly/internal/config"
)

// Free Gmail and Outlook.com are told apart from Workspace and Microsoft 365 however they connect.
func TestConsumerMailbox(t *testing.T) {
	at := func(domain string) string { return "owner" + "@" + domain }
	cases := []struct {
		name     string
		domain   string
		provider string
		mailHost string
		want     bool
	}{
		{"gmail over oauth", "gmail.com", "gmail", "", true},
		{"gmail over imap", "gmail.com", "smtp_imap", "", true},
		{"googlemail", "googlemail.com", "gmail", "", true},
		{"outlook.com over graph", "outlook.com", "outlook", "", true},
		{"hotmail over graph", "hotmail.com", "outlook", "", true},
		{"live.com over graph", "live.com", "outlook", "", true},
		{"workspace", "acme.example", "gmail", "", false},
		{"microsoft 365", "acme.example", "outlook", "", false},
		{"workspace over imap", "acme.example", "smtp_imap", "google_workspace", false},
		{"small host", "acme.example", "smtp_imap", "hostinger", false},
		{"yahoo", "yahoo.com", "smtp_imap", "", false},
	}
	for _, c := range cases {
		m := &Email{Email: at(c.domain), Provider: c.provider, MailHost: c.mailHost}
		if got := m.ConsumerMailbox(); got != c.want {
			t.Errorf("%s: ConsumerMailbox = %v, want %v", c.name, got, c.want)
		}
	}
	var unknown *Email
	if unknown.ConsumerMailbox() {
		t.Error("an unknown mailbox is not a consumer mailbox")
	}
}

func TestNewMailboxVolume(t *testing.T) {
	limit, gap, warmup := NewMailboxVolume("", "outlook", "owner"+"@outlook.com")
	if limit != config.ConsumerCampaignLimitDefault || gap != config.ConsumerMinWaitTimeDefault || warmup != config.ConsumerWarmupMaxDefault {
		t.Errorf("consumer mailbox starts at %d/%d/%d", limit, gap, warmup)
	}
	limit, gap, warmup = NewMailboxVolume("", "gmail", "owner"+"@acme.example")
	if limit != config.CampaignLimitDefault || gap != config.MinWaitTimeDefault || warmup != config.WarmupMaxDefault {
		t.Errorf("workspace mailbox starts at %d/%d/%d", limit, gap, warmup)
	}
}
