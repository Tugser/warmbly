package cipher

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"errors"
)

// ErrDecrypt is the one error Decrypt returns, whatever was wrong with the
// input: malformed, short, tampered or sealed under another key.
var ErrDecrypt = errors.New("cipher: value could not be decrypted")

func (c *Cipher) Decrypt(ctxc context.Context, s string) (string, error) {
	encSecret, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", ErrDecrypt
	}

	block, err := aes.NewCipher(c.plainDEK)
	if err != nil {
		return "", ErrDecrypt
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", ErrDecrypt
	}
	nonceSize := gcm.NonceSize()
	if len(encSecret) < nonceSize+gcm.Overhead() {
		return "", ErrDecrypt
	}
	nonce, ciphertext := encSecret[:nonceSize], encSecret[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", ErrDecrypt
	}

	return string(plaintext), nil
}
