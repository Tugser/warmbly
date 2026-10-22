package encrypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"

	"github.com/warmbly/warmbly/internal/pkg/secretset"
)

// Encrypted holds the hex-encoded ciphertext and nonce.
// It is JSON-marshalable and can be stored in PostgreSQL (JSONB) or Redis.
type Encrypted struct {
	Ciphertext string `json:"ct" db:"ciphertext"` // hex
	Nonce      string `json:"n" db:"nonce"`       // hex
}

// ErrUnreadable is a value shaped like ciphertext that no configured key opens.
var ErrUnreadable = errors.New("sealed value does not open under any configured credential key")

// Encrypter performs AES-GCM encryption with a fixed 32-byte key.
type Encrypter struct {
	aead     cipher.AEAD
	readKeys []cipher.AEAD
}

// FromEnv creates an Encrypter from the CREDENTIALS_ENCRYPTION_KEY env var
// (64 hex chars = 32 bytes). Returns (nil, nil) when the var is unset so
// callers can keep booting without credential sealing configured.
func FromEnv() (*Encrypter, error) {
	keys, err := secretset.Parse(os.Getenv("CREDENTIALS_ENCRYPTION_READ_KEYS"))
	if err != nil {
		return nil, err
	}
	key := os.Getenv("CREDENTIALS_ENCRYPTION_KEY")
	if key == "" {
		if len(keys) > 0 {
			return nil, errors.New("credential read keys require a primary encryption key")
		}
		return nil, nil
	}
	primary, err := NewEncrypterFromHex(key)
	if err != nil {
		return nil, err
	}
	for _, value := range keys {
		if value == key {
			continue
		}
		reader, err := NewEncrypterFromHex(value)
		if err != nil {
			return nil, errors.New("invalid additional credential read key")
		}
		primary.readKeys = append(primary.readKeys, reader.aead)
	}
	return primary, nil
}

// NewEncrypterFromHex creates an Encrypter from a 64-character hex key.
func NewEncrypterFromHex(hexKey string) (*Encrypter, error) {
	// A decoding error quotes the offending byte, which is key material.
	key, err := hex.DecodeString(hexKey)
	if err != nil || len(key) != 32 {
		return nil, errors.New("key must be 32 bytes (64 hex chars)")
	}
	return NewEncrypter(key)
}

// NewEncrypter creates an Encrypter from a raw 32-byte key.
func NewEncrypter(key []byte) (*Encrypter, error) {
	if len(key) != 32 {
		return nil, errors.New("key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Encrypter{aead: aead}, nil
}

// Encrypt returns a hex-encoded ciphertext that contains the nonce prefix.
func (e *Encrypter) Encrypt(plaintext string) (string, error) {
	enc, err := e.EncryptBytes([]byte(plaintext))
	if err != nil {
		return "", err
	}
	return enc.Hex(), nil
}

// Decrypt accepts the hex string produced by Encrypt.
func (e *Encrypter) Decrypt(hexStr string) (string, error) {
	enc, err := ParseHex(hexStr)
	if err != nil {
		return "", err
	}
	plain, err := e.DecryptEncrypted(enc)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// ---------------------------------------------------------------------
// Low-level byte-oriented API (preferred for services/repositories)
// ---------------------------------------------------------------------

// EncryptBytes encrypts a byte slice and returns a structured Encrypted.
func (e *Encrypter) EncryptBytes(plain []byte) (*Encrypted, error) {
	nonce := make([]byte, e.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	ct := e.aead.Seal(nil, nonce, plain, nil)
	return &Encrypted{
		Ciphertext: hex.EncodeToString(ct),
		Nonce:      hex.EncodeToString(nonce),
	}, nil
}

// DecryptEncrypted decrypts a structured Encrypted value.
func (e *Encrypter) DecryptEncrypted(enc *Encrypted) ([]byte, error) {
	nonce, err := hex.DecodeString(enc.Nonce)
	if err != nil {
		return nil, err
	}
	ct, err := hex.DecodeString(enc.Ciphertext)
	if err != nil {
		return nil, err
	}
	if len(nonce) != e.aead.NonceSize() {
		return nil, errors.New("invalid encryption nonce")
	}
	plaintext, err := e.aead.Open(nil, nonce, ct, nil)
	if err == nil {
		return plaintext, nil
	}
	for _, reader := range e.readKeys {
		if plaintext, readErr := reader.Open(nil, nonce, ct, nil); readErr == nil {
			return plaintext, nil
		}
	}
	return nil, err
}

// Reseal returns stored sealed under the primary key when only a read key opens it; changed is false when the primary already does.
func (e *Encrypter) Reseal(stored string) (resealed string, changed bool, err error) {
	enc, err := ParseHex(stored)
	if err != nil {
		return "", false, ErrUnreadable
	}
	nonce, nerr := hex.DecodeString(enc.Nonce)
	ct, cerr := hex.DecodeString(enc.Ciphertext)
	if nerr != nil || cerr != nil {
		return "", false, ErrUnreadable
	}
	if _, err := e.aead.Open(nil, nonce, ct, nil); err == nil {
		return "", false, nil
	}
	for _, reader := range e.readKeys {
		plain, err := reader.Open(nil, nonce, ct, nil)
		if err != nil {
			continue
		}
		sealed, err := e.EncryptBytes(plain)
		if err != nil {
			return "", false, err
		}
		return sealed.Hex(), true, nil
	}
	return "", false, ErrUnreadable
}

// LooksSealed reports whether stored has the shape Encrypt produces: hex nonce, ciphertext and a full GCM tag.
func LooksSealed(stored string) bool {
	if len(stored)%2 != 0 || len(stored) < 2*(12+16) {
		return false
	}
	_, err := hex.DecodeString(stored)
	return err == nil
}

// ---------------------------------------------------------------------
// Helper for the old "single hex string" format
// ---------------------------------------------------------------------

// Hex returns ciphertext||nonce as a single hex string (same as your original code).
func (e *Encrypted) Hex() string {
	// nonce (12 bytes) + ciphertext
	rawNonce, _ := hex.DecodeString(e.Nonce)
	rawCt, _ := hex.DecodeString(e.Ciphertext)
	return hex.EncodeToString(append(rawNonce, rawCt...))
}

// ParseHex restores an Encrypted from the single-hex format.
func ParseHex(hexStr string) (*Encrypted, error) {
	data, err := hex.DecodeString(hexStr)
	if err != nil {
		return nil, err
	}
	nonceSize := 12 // GCM standard nonce
	if len(data) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}
	return &Encrypted{
		Nonce:      hex.EncodeToString(data[:nonceSize]),
		Ciphertext: hex.EncodeToString(data[nonceSize:]),
	}, nil
}
