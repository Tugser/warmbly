// Package legacysecret counts reads of secrets still stored in their pre-sealing plaintext form.
package legacysecret

import (
	"sync"
	"sync/atomic"

	"github.com/rs/zerolog/log"
)

// Kinds of stored secret that can still be read in plaintext.
const (
	MailboxCredential     = "mailbox_credential"
	WebhookSecret         = "webhook_signing_secret"
	OAuthAppWebhookSecret = "oauth_app_webhook_secret"
)

var counts sync.Map // kind -> *atomic.Int64

// Read records one plaintext read of kind; the value itself is never seen here.
func Read(kind string) {
	v, _ := counts.LoadOrStore(kind, new(atomic.Int64))
	n := v.(*atomic.Int64).Add(1)
	log.Warn().Str("kind", kind).Int64("legacy_reads", n).Msg("legacy secret: read a value stored before sealing")
}

// Count is the number of plaintext reads of kind since the process started.
func Count(kind string) int64 {
	if v, ok := counts.Load(kind); ok {
		return v.(*atomic.Int64).Load()
	}
	return 0
}

// ResealFailed records that a plaintext value could not be resealed; only the failure class is logged.
func ResealFailed(kind, class string) {
	log.Error().Str("kind", kind).Str("class", class).Msg("legacy secret: could not reseal a value stored before sealing; the next read retries")
}
