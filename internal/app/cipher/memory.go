package cipher

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	// MemoryKeyTTL bounds how long a process holds a plaintext DEK without asking Redis or KMS again.
	MemoryKeyTTL = 5 * time.Minute
	// memoryKeyLimit caps the organizations held at once; past it the oldest entry is dropped.
	memoryKeyLimit = 4096
)

type memoryEntry struct {
	key     []byte
	expires time.Time
}

// memoryKeys is the per-process DEK cache in front of Redis, so a hot organization costs no round trip.
type memoryKeys struct {
	mu      sync.Mutex
	entries map[uuid.UUID]memoryEntry
	ttl     time.Duration
	limit   int
	now     func() time.Time
}

func newMemoryKeys() *memoryKeys {
	return &memoryKeys{entries: map[uuid.UUID]memoryEntry{}, ttl: MemoryKeyTTL, limit: memoryKeyLimit, now: time.Now}
}

func (m *memoryKeys) get(orgID uuid.UUID) []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[orgID]
	if !ok {
		return nil
	}
	if !m.now().Before(e.expires) {
		delete(m.entries, orgID)
		return nil
	}
	return e.key
}

func (m *memoryKeys) put(orgID uuid.UUID, key []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	if _, ok := m.entries[orgID]; !ok && len(m.entries) >= m.limit {
		m.evictLocked(now)
	}
	m.entries[orgID] = memoryEntry{key: key, expires: now.Add(m.ttl)}
}

// evictLocked drops every expired entry, or the one closest to expiry when none has.
func (m *memoryKeys) evictLocked(now time.Time) {
	var oldest uuid.UUID
	var oldestAt time.Time
	removed := false
	for id, e := range m.entries {
		if !now.Before(e.expires) {
			delete(m.entries, id)
			removed = true
			continue
		}
		if oldestAt.IsZero() || e.expires.Before(oldestAt) {
			oldest, oldestAt = id, e.expires
		}
	}
	if !removed && !oldestAt.IsZero() {
		delete(m.entries, oldest)
	}
}
