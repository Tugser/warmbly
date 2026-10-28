package cipher

import (
	"bytes"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestDecryptRefusesEveryBadInputWithOneError(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	c, err := NewStatic(key).Cipher(t.Context(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := c.Encrypt(t.Context(), "Quarterly pricing for Acme")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte(nil), raw...)
	tampered[len(tampered)-1] ^= 1
	other, err := NewStatic(bytes.Repeat([]byte{9}, 32)).Cipher(t.Context(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string]struct {
		c     *Cipher
		input string
	}{
		"malformed base64": {c, "not base64 at all!"},
		"empty":            {c, ""},
		"nonce only":       {c, base64.StdEncoding.EncodeToString(raw[:12])},
		"short tag":        {c, base64.StdEncoding.EncodeToString(raw[:20])},
		"tampered":         {c, base64.StdEncoding.EncodeToString(tampered)},
		"other key":        {other, sealed},
		"bad key length":   {&Cipher{plainDEK: []byte("short")}, sealed},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			plain, err := tc.c.Decrypt(t.Context(), tc.input)
			if !errors.Is(err, ErrDecrypt) || err.Error() != ErrDecrypt.Error() || plain != "" {
				t.Fatalf("Decrypt = %q, %v; want ErrDecrypt", plain, err)
			}
		})
	}
	if plain, err := c.Decrypt(t.Context(), sealed); err != nil || plain != "Quarterly pricing for Acme" {
		t.Fatalf("round trip = %q, %v", plain, err)
	}
}
