package cipher

import (
	"bytes"
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/infrastructure/encryptedkeys"
)

// raceStore reports no DEK on the first read and then holds the one a concurrent caller stored.
type raceStore struct {
	mu    sync.Mutex
	reads int
	blob  string
	puts  []string
}

func (s *raceStore) Put(_ context.Context, _ uuid.UUID, b string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.puts = append(s.puts, b)
	if s.blob != "" {
		return encryptedkeys.ErrAlreadyExists
	}
	s.blob = b
	return nil
}

func (s *raceStore) Get(_ context.Context, _ uuid.UUID) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	if s.reads == 1 {
		return "", nil
	}
	return s.blob, nil
}

func (s *raceStore) Delete(context.Context, uuid.UUID) error { return nil }
func (s *raceStore) Name() string                            { return "race" }

type fakeKMS struct{ keys map[string][]byte }

func (k *fakeKMS) GenerateDataKey(context.Context) ([]byte, string, error) {
	return bytes.Repeat([]byte{2}, 32), "loser-blob", nil
}

func (k *fakeKMS) GetDecryptedKey(_ context.Context, b string) ([]byte, error) {
	return k.keys[b], nil
}

func (k *fakeKMS) Name() string { return "fake" }

func TestCipherAdoptsDEKStoredByConcurrentCreator(t *testing.T) {
	winner := bytes.Repeat([]byte{1}, 32)
	store := &raceStore{blob: "winner-blob"}
	svc := &cipherService{encryptedKeys: store, kms: &fakeKMS{keys: map[string][]byte{"winner-blob": winner}}, memory: newMemoryKeys()}

	c, err := svc.Cipher(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("Cipher: %v", err)
	}
	if !bytes.Equal(c.plainDEK, winner) {
		t.Fatal("cipher did not adopt the stored DEK")
	}
	if store.blob != "winner-blob" {
		t.Fatalf("stored DEK was replaced: %q", store.blob)
	}
}
