package cipher

import (
	"context"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/infrastructure/cache"
	"github.com/warmbly/warmbly/internal/infrastructure/encryptedkeys"
	"github.com/warmbly/warmbly/internal/infrastructure/kms"
	"golang.org/x/sync/singleflight"
)

type CipherService interface {
	Cipher(ctx context.Context, orgID uuid.UUID) (*Cipher, error)
}

type cipherService struct {
	encryptedKeys encryptedkeys.Store
	cache         *cache.Cache
	kms           kms.Provider
	memory        *memoryKeys
	flight        singleflight.Group
}

func NewService(kms kms.Provider, cache *cache.Cache, encryptedKeys encryptedkeys.Store) CipherService {
	return &cipherService{
		kms:           kms,
		cache:         cache,
		encryptedKeys: encryptedKeys,
		memory:        newMemoryKeys(),
	}
}
