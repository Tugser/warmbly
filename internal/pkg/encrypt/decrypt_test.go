package encrypt

import (
	"errors"
	"strings"
	"testing"
)

func TestDecryptRefusesEveryBadInputWithOneError(t *testing.T) {
	e, err := NewEncrypterFromHex(strings.Repeat("11", 32))
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewEncrypterFromHex(strings.Repeat("22", 32))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := e.Encrypt("mailbox-password")
	if err != nil {
		t.Fatal(err)
	}
	last := sealed[len(sealed)-1:]
	flipped := "0"
	if last == "0" {
		flipped = "1"
	}
	for name, input := range map[string]string{
		"not hex":   "zz" + sealed[2:],
		"odd hex":   sealed[:len(sealed)-1],
		"empty":     "",
		"short":     sealed[:16],
		"no tag":    sealed[:24+8],
		"tampered":  sealed[:len(sealed)-1] + flipped,
		"plaintext": "mailbox-password",
	} {
		t.Run(name, func(t *testing.T) {
			plain, err := e.Decrypt(input)
			if !errors.Is(err, ErrDecrypt) || err.Error() != ErrDecrypt.Error() || plain != "" {
				t.Fatalf("Decrypt = %q, %v; want ErrDecrypt", plain, err)
			}
		})
	}
	if _, err := other.Decrypt(sealed); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("other key: %v, want ErrDecrypt", err)
	}
	if _, err := e.DecryptEncrypted(&Encrypted{Nonce: "zz", Ciphertext: "00"}); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("DecryptEncrypted: %v, want ErrDecrypt", err)
	}
	if plain, err := e.Decrypt(sealed); err != nil || plain != "mailbox-password" {
		t.Fatalf("round trip = %q, %v", plain, err)
	}
}
