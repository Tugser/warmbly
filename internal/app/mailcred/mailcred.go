// Package mailcred seals mailbox credentials (SMTP/IMAP logins, OAuth tokens)
// at rest. New values go under the organization's KMS-wrapped data key; values
// sealed under the instance CREDENTIALS_ENCRYPTION_KEY stay readable.
package mailcred

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/app/cipher"
	"github.com/warmbly/warmbly/internal/pkg/encrypt"
)

// OrgPrefix marks a value sealed under the organization data key; instance ciphertext is hex and never contains ':'.
const OrgPrefix = "dek1:"

// Mode chooses which key new mailbox credentials are sealed under.
type Mode string

const (
	// ModeInstance seals under CREDENTIALS_ENCRYPTION_KEY and ships plaintext to workers.
	ModeInstance Mode = "instance"
	// ModeOrganization seals under the organization data key, at rest and on the bus.
	ModeOrganization Mode = "organization"
)

// EnvMode is the setting that selects the Mode.
const EnvMode = "MAILBOX_CREDENTIALS_SEALING"

// ErrNoKey is returned when the key a value needs is not configured.
var ErrNoKey = errors.New("mailbox credential key not configured (set CREDENTIALS_ENCRYPTION_KEY)")

// ParseMode reads a Mode, treating empty as ModeInstance.
func ParseMode(v string) (Mode, error) {
	switch Mode(strings.ToLower(strings.TrimSpace(v))) {
	case "", ModeInstance:
		return ModeInstance, nil
	case ModeOrganization:
		return ModeOrganization, nil
	}
	return "", fmt.Errorf("%s must be %q or %q", EnvMode, ModeInstance, ModeOrganization)
}

// ModeFromEnv reads MAILBOX_CREDENTIALS_SEALING.
func ModeFromEnv() (Mode, error) {
	return ParseMode(os.Getenv(EnvMode))
}

// IsOrgSealed reports whether a stored value is sealed under an organization data key.
func IsOrgSealed(stored string) bool {
	return strings.HasPrefix(stored, OrgPrefix)
}

// SealOrg seals plain under an organization cipher.
func SealOrg(ctx context.Context, c *cipher.Cipher, plain string) (string, error) {
	if c == nil {
		return "", errors.New("organization key unavailable")
	}
	sealed, err := c.Encrypt(ctx, plain)
	if err != nil {
		return "", err
	}
	return OrgPrefix + sealed, nil
}

// OpenWith opens a stored value of either format; orgCipher is only consulted for an organization-sealed value.
func OpenWith(ctx context.Context, orgCipher *cipher.Cipher, instance *encrypt.Encrypter, stored string) (string, error) {
	if IsOrgSealed(stored) {
		body := strings.TrimPrefix(stored, OrgPrefix)
		if body == "" {
			return "", nil
		}
		if orgCipher == nil {
			return "", errors.New("organization key unavailable")
		}
		return orgCipher.Decrypt(ctx, body)
	}
	if instance == nil {
		return "", ErrNoKey
	}
	return instance.Decrypt(stored)
}

// Sealer seals and opens mailbox credentials for one organization at a time.
type Sealer struct {
	Instance *encrypt.Encrypter
	Orgs     cipher.CipherService
	Mode     Mode
}

// NewSealer builds a Sealer; ModeOrganization requires a cipher service.
func NewSealer(instance *encrypt.Encrypter, orgs cipher.CipherService, mode Mode) (*Sealer, error) {
	if mode == ModeOrganization && orgs == nil {
		return nil, fmt.Errorf("%s=%s needs the organization key service", EnvMode, mode)
	}
	return &Sealer{Instance: instance, Orgs: orgs, Mode: mode}, nil
}

// FromEnv builds the Sealer MAILBOX_CREDENTIALS_SEALING selects.
func FromEnv(instance *encrypt.Encrypter, orgs cipher.CipherService) (*Sealer, error) {
	mode, err := ModeFromEnv()
	if err != nil {
		return nil, err
	}
	return NewSealer(instance, orgs, mode)
}

// SealsForOrganizations reports whether new credentials go under organization keys, on disk and to workers.
func (s *Sealer) SealsForOrganizations() bool {
	return s != nil && s.Mode == ModeOrganization && s.Orgs != nil
}

// Cipher resolves an organization's cipher.
func (s *Sealer) Cipher(ctx context.Context, orgID uuid.UUID) (*cipher.Cipher, error) {
	if s == nil || s.Orgs == nil {
		return nil, errors.New("organization key unavailable")
	}
	return s.Orgs.Cipher(ctx, orgID)
}

// Seal seals plain for storage. A mailbox with no organization stays under the instance key.
func (s *Sealer) Seal(ctx context.Context, orgID *uuid.UUID, plain string) (string, error) {
	if s == nil {
		return "", ErrNoKey
	}
	if s.SealsForOrganizations() && orgID != nil && *orgID != uuid.Nil {
		c, err := s.Orgs.Cipher(ctx, *orgID)
		if err != nil {
			return "", err
		}
		return SealOrg(ctx, c, plain)
	}
	if s.Instance == nil {
		return "", ErrNoKey
	}
	return s.Instance.Encrypt(plain)
}

// SealAll seals several values with one key lookup.
func (s *Sealer) SealAll(ctx context.Context, orgID *uuid.UUID, plains ...string) ([]string, error) {
	out := make([]string, len(plains))
	if s.SealsForOrganizations() && orgID != nil && *orgID != uuid.Nil {
		c, err := s.Orgs.Cipher(ctx, *orgID)
		if err != nil {
			return nil, err
		}
		for i, p := range plains {
			if out[i], err = SealOrg(ctx, c, p); err != nil {
				return nil, err
			}
		}
		return out, nil
	}
	for i, p := range plains {
		v, err := s.Seal(ctx, orgID, p)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

// Open returns the plaintext behind a stored value of either format.
func (s *Sealer) Open(ctx context.Context, orgID *uuid.UUID, stored string) (string, error) {
	if s == nil {
		return "", ErrNoKey
	}
	var c *cipher.Cipher
	if IsOrgSealed(stored) && stored != OrgPrefix {
		if orgID == nil || *orgID == uuid.Nil || s.Orgs == nil {
			return "", errors.New("organization-sealed credential without an organization key")
		}
		var err error
		if c, err = s.Orgs.Cipher(ctx, *orgID); err != nil {
			return "", err
		}
	}
	return OpenWith(ctx, c, s.Instance, stored)
}

// OpenOrLegacy is Open for columns that predate sealing: a value no key opens is returned verbatim with legacy set.
func (s *Sealer) OpenOrLegacy(ctx context.Context, orgID *uuid.UUID, stored string) (plain string, legacy bool, err error) {
	if IsOrgSealed(stored) {
		plain, err = s.Open(ctx, orgID, stored)
		return plain, false, err
	}
	if s == nil || s.Instance == nil {
		return "", false, ErrNoKey
	}
	if plain, err := s.Instance.Decrypt(stored); err == nil {
		return plain, false, nil
	}
	// Sealed-format values no key opens need their key restored, never a reseal as plaintext.
	if encrypt.LooksSealed(stored) {
		return "", false, encrypt.ErrUnreadable
	}
	return stored, true, nil
}
