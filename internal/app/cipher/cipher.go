package cipher

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/infrastructure/encryptedkeys"
	"github.com/warmbly/warmbly/internal/observability/errs"
)

// dekLookupTimeout bounds one shared Redis and KMS lookup of an organization key.
const dekLookupTimeout = 30 * time.Second

type Cipher struct {
	plainDEK []byte
}

func (s *cipherService) Cipher(ctx context.Context, orgID uuid.UUID) (*Cipher, error) {
	if key := s.memory.get(orgID); key != nil {
		return &Cipher{plainDEK: key}, nil
	}
	// Concurrent first uses of one organization share a single Redis read and KMS call.
	v, err, _ := s.flight.Do(orgID.String(), func() (any, error) {
		// Shared by every waiter, so it must not end with the first caller's context.
		lookupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), dekLookupTimeout)
		defer cancel()
		key, err := s.resolveDEK(lookupCtx, orgID)
		if err != nil {
			return nil, err
		}
		s.memory.put(orgID, key)
		return key, nil
	})
	if err != nil {
		return nil, err
	}
	return &Cipher{plainDEK: v.([]byte)}, nil
}

func (s *cipherService) resolveDEK(ctx context.Context, orgID uuid.UUID) ([]byte, error) {
	// Any miss or cache error (redis.Nil on first use) falls through to KMS; a cache problem must never block crypto.
	if key, err := s.getDecryptedKey(ctx, orgID); err == nil && len(key) > 0 {
		return key, nil
	}

	key, err := s.loadOrCreateDEK(ctx, orgID)
	if err != nil {
		return nil, err
	}

	if err := s.saveDecryptedKey(ctx, orgID, key); err != nil {
		errs.CaptureException(err)
	}
	return key, nil
}

// loadOrCreateDEK returns the organization's stored DEK, creating it on first
// use. The stored key always wins: a concurrent first use adopts it.
func (s *cipherService) loadOrCreateDEK(ctx context.Context, orgID uuid.UUID) ([]byte, error) {
	encDEKB64, err := s.encryptedKeys.Get(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if encDEKB64 != "" {
		return s.kms.GetDecryptedKey(ctx, encDEKB64)
	}

	key, encryptedDEK, err := s.kms.GenerateDataKey(ctx)
	if err != nil {
		return nil, err
	}
	err = s.encryptedKeys.Put(ctx, orgID, encryptedDEK)
	if err == nil {
		return key, nil
	}
	if !errors.Is(err, encryptedkeys.ErrAlreadyExists) {
		return nil, err
	}

	// Another caller stored this organization's DEK first; ours is discarded unused.
	encDEKB64, err = s.encryptedKeys.Get(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if encDEKB64 == "" {
		return nil, fmt.Errorf("cipher: dek for organization %s reported present but not readable", orgID)
	}
	return s.kms.GetDecryptedKey(ctx, encDEKB64)
}
