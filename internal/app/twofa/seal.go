package twofa

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
)

// DeriveKey turns the configured TWOFA_SECRET into a fixed 32-byte AES key.
// A server-wide key (not the per-user DEK) is required because the TOTP secret
// must be decryptable DURING login, before the user's DEK is reachable.
func DeriveKey(secret string) [32]byte {
	return sha256.Sum256([]byte(secret))
}

// Seal encrypts plaintext with AES-256-GCM under key; output is base64 of
// nonce||ciphertext.
func Seal(key [32]byte, plaintext string) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ct), nil
}

// Open reverses Seal.
func Open(key [32]byte, ciphertext string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("ciphertext too short")
	}
	nonce, ct := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// ErrUnreadableSecret is a sealed TOTP secret that no configured MFA key opens.
var ErrUnreadableSecret = errors.New("sealed TOTP secret does not open under any configured MFA key")

// Reseal returns sealed under primary when only a read key opens it; changed is false when primary already does.
func Reseal(primary [32]byte, reads [][32]byte, sealed string) (resealed string, changed bool, err error) {
	if _, err := Open(primary, sealed); err == nil {
		return "", false, nil
	}
	for _, key := range reads {
		plain, err := Open(key, sealed)
		if err != nil {
			continue
		}
		resealed, err := Seal(primary, plain)
		if err != nil {
			return "", false, err
		}
		return resealed, true, nil
	}
	return "", false, ErrUnreadableSecret
}

func newGCM(key [32]byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
