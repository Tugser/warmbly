package twofa

import (
	"testing"
	"time"
)

func TestMFAKeyRotationPreservesEnrollmentAndRollback(t *testing.T) {
	oldKey, newKey := DeriveKey("previous-mfa-secret"), DeriveKey("replacement-mfa-secret")
	secret, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	before, err := Seal(oldKey, secret)
	if err != nil {
		t.Fatal(err)
	}
	after, err := Seal(newKey, secret)
	if err != nil {
		t.Fatal(err)
	}
	baseline := &service{sealKey: oldKey, readKeys: [][32]byte{newKey}}
	current := &service{sealKey: newKey, readKeys: [][32]byte{oldKey}}
	for _, reader := range []*service{baseline, current} {
		for _, sealed := range []string{before, after} {
			got, err := reader.openSecret(sealed)
			if err != nil || got != secret {
				t.Fatal("MFA enrollment must survive resealing and rollback")
			}
			code, codeErr := hotp(secret, uint64(time.Now().Unix())/totpPeriod)
			if codeErr != nil {
				t.Fatal(codeErr)
			}
			if _, ok := ValidateCodeStep(got, code); !ok {
				t.Fatal("the existing authenticator must remain usable")
			}
		}
	}
	if _, err := Open(oldKey, after); err == nil {
		t.Fatal("replacement ciphertext must require the replacement key")
	}
}

func TestResealMovesEnrollmentsToThePrimaryMFAKey(t *testing.T) {
	oldKey, newKey := DeriveKey("previous-mfa-secret"), DeriveKey("replacement-mfa-secret")
	before, err := Seal(oldKey, "JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatal(err)
	}
	resealed, changed, err := Reseal(newKey, [][32]byte{oldKey}, before)
	if err != nil || !changed {
		t.Fatalf("a read-key enrollment must be resealed: changed=%v err=%v", changed, err)
	}
	if got, err := Open(newKey, resealed); err != nil || got != "JBSWY3DPEHPK3PXP" {
		t.Fatal("a resealed enrollment must open under the primary alone")
	}
	if _, changed, err := Reseal(newKey, [][32]byte{oldKey}, resealed); err != nil || changed {
		t.Fatal("an enrollment already under the primary must be left alone")
	}
	if _, _, err := Reseal(newKey, nil, before); err != ErrUnreadableSecret {
		t.Fatal("an enrollment no configured key opens must be reported unreadable")
	}
}
