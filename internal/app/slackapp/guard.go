package slackapp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// releaseScript deletes a lock only while it still holds this holder's token.
var releaseScript = redis.NewScript(`if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("DEL", KEYS[1]) end return 0`)

// guard dedupes Slack deliveries and serializes runs per thread. Redis makes
// it hold across backend replicas; without Redis it holds per process.
type guard struct {
	rdb *redis.Client

	mu    sync.Mutex
	marks map[string]time.Time // key -> expiry
	vals  map[string]memVal
}

type memVal struct {
	v   string
	exp time.Time
}

func newGuard(rdb *redis.Client) *guard {
	return &guard{rdb: rdb, marks: map[string]time.Time{}, vals: map[string]memVal{}}
}

// put stores a short-lived value (a draft), in Redis when available.
func (g *guard) put(ctx context.Context, key, val string, ttl time.Duration) {
	if g.rdb != nil && g.rdb.Set(ctx, key, val, ttl).Err() == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.vals) > 2000 {
		now := time.Now()
		for k, v := range g.vals {
			if !v.exp.After(now) {
				delete(g.vals, k)
			}
		}
	}
	g.vals[key] = memVal{v: val, exp: time.Now().Add(ttl)}
}

func (g *guard) get(ctx context.Context, key string) string {
	if g.rdb != nil {
		if v, err := g.rdb.Get(ctx, key).Result(); err == nil {
			return v
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if v, ok := g.vals[key]; ok && v.exp.After(time.Now()) {
		return v.v
	}
	return ""
}

// takeScript reads and deletes a key in one step, so only one caller gets its value.
var takeScript = redis.NewScript(`local v = redis.call("GET", KEYS[1]) if v then redis.call("DEL", KEYS[1]) end return v`)

// take returns key's value and deletes it atomically; concurrent callers get it at most once.
func (g *guard) take(ctx context.Context, key string) string {
	if g.rdb != nil {
		if v, err := takeScript.Run(ctx, g.rdb, []string{key}).Text(); err == nil && v != "" {
			return v
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	v, ok := g.vals[key]
	delete(g.vals, key)
	if ok && v.exp.After(time.Now()) {
		return v.v
	}
	return ""
}

func (g *guard) del(ctx context.Context, key string) {
	if g.rdb != nil {
		_ = g.rdb.Del(ctx, key).Err()
	}
	g.mu.Lock()
	delete(g.vals, key)
	g.mu.Unlock()
}

// first reports whether key is new for ttl. A Redis error fails open, since a
// rare duplicate beats a dropped message.
func (g *guard) first(ctx context.Context, key string, ttl time.Duration) bool {
	if g.rdb != nil {
		ok, err := g.rdb.SetNX(ctx, key, "1", ttl).Result()
		if err == nil {
			return ok
		}
	}
	return g.memSet(key, ttl)
}

// lock takes key for ttl and returns its release, or false when held. With
// Redis configured an error refuses the lock: a per-process fallback would let
// two replicas run the same thread.
func (g *guard) lock(ctx context.Context, key string, ttl time.Duration) (func(), bool) {
	if g.rdb != nil {
		token := randomHex(16)
		ok, err := g.rdb.SetNX(ctx, key, token, ttl).Result()
		if err != nil || !ok {
			return nil, false
		}
		return func() {
			rctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = releaseScript.Run(rctx, g.rdb, []string{key}, token).Err()
		}, true
	}
	if !g.memSet(key, ttl) {
		return nil, false
	}
	return func() {
		g.mu.Lock()
		delete(g.marks, key)
		g.mu.Unlock()
	}, true
}

func (g *guard) memSet(key string, ttl time.Duration) bool {
	now := time.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	if exp, ok := g.marks[key]; ok && exp.After(now) {
		return false
	}
	if len(g.marks) > 10000 {
		for k, exp := range g.marks {
			if !exp.After(now) {
				delete(g.marks, k)
			}
		}
	}
	g.marks[key] = now.Add(ttl)
	return true
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
