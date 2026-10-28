package imap

import (
	"strings"
	"sync"
	"time"

	goimap "github.com/emersion/go-imap/v2"
	"github.com/warmbly/warmbly/internal/client/netbind"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

const loginCooldownDuration = 5 * time.Minute

type loginEndpoint struct {
	host, source string
	port         int
}

// IP bans apply to other mailboxes using this endpoint and worker egress too.
var loginIPCooldowns = struct {
	sync.Mutex
	until map[loginEndpoint]time.Time
}{until: make(map[loginEndpoint]time.Time)}

func (c *Client) loginEndpoint() (loginEndpoint, bool) {
	var host string
	var port int
	switch {
	case c.AuthType == models.AuthPlain && c.Credentials != nil:
		host, port = c.Credentials.Host, c.Credentials.Port
	case c.AuthType == models.AuthOAuth2 && c.Oauth2 != nil:
		host, port = c.Oauth2.Host, c.Oauth2.Port
	default:
		return loginEndpoint{}, false
	}
	if host == "" {
		return loginEndpoint{}, false
	}
	key := loginEndpoint{host: strings.ToLower(models.NormalizeMailHost(host)), port: port}
	bind := c.BindIP
	if bind == nil {
		bind = netbind.FromEnv()
	}
	if bind != nil {
		key.source = bind.IP.String() + "%" + bind.Zone
	}
	return key, true
}

func loginCooldownError(delay time.Duration) *errx.MailError {
	err := errx.MError(errx.MailErrorWarning, errx.MailErrorCodeSendingTooFast,
		"The IMAP server temporarily limited sign-in attempts. Warmbly will retry automatically.", errx.MailErrorResolveMethodRetry)
	err.RetryAfter = delay
	return err
}

func (c *Client) loginThrottle(err *goimap.Error) *errx.MailError {
	if err.Type != goimap.StatusResponseTypeNo && err.Type != goimap.StatusResponseTypeBye {
		return nil
	}
	text := strings.ToLower(strings.Join(strings.Fields(err.Text), " "))
	ipBan := strings.Contains(text, "ip temp banned")
	if !ipBan && !(strings.Contains(text, "login denied") && strings.Contains(text, "too many failures")) &&
		!strings.Contains(text, "too many login attempts") && !strings.Contains(text, "too many authentication failures") {
		return nil
	}
	now := time.Now()
	until := now.Add(loginCooldownDuration)
	c.loginRetryAt.Store(until.UnixNano())
	if key, ok := c.loginEndpoint(); ok && ipBan {
		loginIPCooldowns.Lock()
		for endpoint, deadline := range loginIPCooldowns.until {
			if !deadline.After(now) {
				delete(loginIPCooldowns.until, endpoint)
			}
		}
		// Keep arbitrary provider endpoints from growing process memory without bound.
		if len(loginIPCooldowns.until) < 1024 || !loginIPCooldowns.until[key].IsZero() {
			loginIPCooldowns.until[key] = until
		}
		loginIPCooldowns.Unlock()
	}
	return loginCooldownError(loginCooldownDuration)
}

func (c *Client) loginCooldown(now time.Time) *errx.MailError {
	until := time.Unix(0, c.loginRetryAt.Load())
	if key, ok := c.loginEndpoint(); ok {
		loginIPCooldowns.Lock()
		deadline := loginIPCooldowns.until[key]
		if !deadline.After(now) {
			delete(loginIPCooldowns.until, key)
		} else if deadline.After(until) {
			until = deadline
		}
		loginIPCooldowns.Unlock()
	}
	if until.After(now) {
		return loginCooldownError(until.Sub(now))
	}
	return nil
}
