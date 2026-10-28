package cache

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// reserveAttemptScript counts and opens the window in one step, so no key is left without an expiry.
var reserveAttemptScript = redis.NewScript(`
local n = redis.call("INCR", KEYS[1])
if redis.call("PTTL", KEYS[1]) < 0 then redis.call("PEXPIRE", KEYS[1], ARGV[1]) end
return n`)

// releaseAttemptScript refunds one attempt and drops a key that reaches zero.
var releaseAttemptScript = redis.NewScript(`
local n = redis.call("DECR", KEYS[1])
if n <= 0 then redis.call("DEL", KEYS[1]) end
return n`)

// ReserveAttempt charges one attempt to key before the secret is compared and
// reports whether it is within limit for the window. On error the result is
// false, and a verifier refuses rather than compare an uncharged attempt.
func (c *Cache) ReserveAttempt(ctx context.Context, key string, limit int64, window time.Duration) (bool, error) {
	ms := window.Milliseconds()
	if ms < 1 {
		ms = 1
	}
	n, err := reserveAttemptScript.Run(ctx, c.Client, []string{key}, ms).Int64()
	if err != nil {
		return false, err
	}
	return n <= limit, nil
}

// ReleaseAttempt refunds an attempt that ended before any secret was compared.
func (c *Cache) ReleaseAttempt(ctx context.Context, key string) error {
	return releaseAttemptScript.Run(ctx, c.Client, []string{key}).Err()
}
