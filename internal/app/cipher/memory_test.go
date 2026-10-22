package cipher

import (
	"bytes"
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// countingKMS counts unwraps and blocks each one until released, so concurrent callers overlap.
type countingKMS struct {
	calls   atomic.Int32
	release chan struct{}
}

func (k *countingKMS) GenerateDataKey(context.Context) ([]byte, string, error) {
	return bytes.Repeat([]byte{3}, 32), "blob", nil
}

func (k *countingKMS) GetDecryptedKey(context.Context, string) ([]byte, error) {
	k.calls.Add(1)
	if k.release != nil {
		<-k.release
	}
	return bytes.Repeat([]byte{4}, 32), nil
}

func (k *countingKMS) Name() string { return "counting" }

type fixedStore struct{}

func (fixedStore) Put(context.Context, uuid.UUID, string) error   { return nil }
func (fixedStore) Get(context.Context, uuid.UUID) (string, error) { return "blob", nil }
func (fixedStore) Delete(context.Context, uuid.UUID) error        { return nil }
func (fixedStore) Name() string                                   { return "fixed" }

func TestCipherServesRepeatUsesFromMemory(t *testing.T) {
	kms := &countingKMS{}
	svc := &cipherService{encryptedKeys: fixedStore{}, kms: kms, memory: newMemoryKeys()}
	org := uuid.New()
	for range 5 {
		if _, err := svc.Cipher(context.Background(), org); err != nil {
			t.Fatal(err)
		}
	}
	if got := kms.calls.Load(); got != 1 {
		t.Fatalf("five uses of one organization cost %d KMS calls, want 1", got)
	}
}

func TestCipherCoalescesConcurrentFirstUse(t *testing.T) {
	kms := &countingKMS{release: make(chan struct{})}
	svc := &cipherService{encryptedKeys: fixedStore{}, kms: kms, memory: newMemoryKeys()}
	org := uuid.New()

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := svc.Cipher(context.Background(), org); err != nil {
				t.Error(err)
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(kms.release)
	wg.Wait()
	if got := kms.calls.Load(); got != 1 {
		t.Fatalf("concurrent first use cost %d KMS calls, want 1", got)
	}
}

func TestMemoryKeysExpireAndStayBounded(t *testing.T) {
	now := time.Unix(0, 0)
	m := &memoryKeys{entries: map[uuid.UUID]memoryEntry{}, ttl: time.Minute, limit: 2, now: func() time.Time { return now }}
	a, b, c := uuid.New(), uuid.New(), uuid.New()

	m.put(a, []byte("a"))
	now = now.Add(time.Second)
	m.put(b, []byte("b"))
	m.put(c, []byte("c"))
	if len(m.entries) != 2 || m.get(a) != nil {
		t.Fatal("a full cache must drop the entry closest to expiry")
	}
	if m.get(b) == nil || m.get(c) == nil {
		t.Fatal("newer entries must survive eviction")
	}

	now = now.Add(time.Minute)
	if m.get(b) != nil {
		t.Fatal("an expired key must not be served")
	}
}
