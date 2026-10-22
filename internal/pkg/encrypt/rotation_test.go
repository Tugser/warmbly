package encrypt

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCredentialRotationPreservesMixedWritesAndRollback(t *testing.T) {
	oldKey, newKey := strings.Repeat("11", 32), strings.Repeat("22", 32)
	original, err := NewEncrypterFromHex(oldKey)
	if err != nil {
		t.Fatal(err)
	}
	before, err := original.Encrypt("existing-mailbox-password")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CREDENTIALS_ENCRYPTION_KEY", oldKey)
	reads, _ := json.Marshal([]string{newKey})
	t.Setenv("CREDENTIALS_ENCRYPTION_READ_KEYS", string(reads))
	baseline, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	baselineWrite, err := baseline.Encrypt("baseline-password")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := original.Decrypt(baselineWrite); err != nil || got != "baseline-password" {
		t.Fatal("baseline must retain the original write format and key")
	}
	t.Setenv("CREDENTIALS_ENCRYPTION_KEY", newKey)
	reads, _ = json.Marshal([]string{oldKey})
	t.Setenv("CREDENTIALS_ENCRYPTION_READ_KEYS", string(reads))
	replacement, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	after, err := replacement.Encrypt("new-mailbox-password")
	if err != nil {
		t.Fatal(err)
	}
	for _, reader := range []*Encrypter{baseline, replacement} {
		for _, sample := range []struct{ sealed, want string }{{before, "existing-mailbox-password"}, {after, "new-mailbox-password"}} {
			if got, err := reader.Decrypt(sample.sealed); err != nil || got != sample.want {
				t.Fatal("compatible releases must read both generations")
			}
		}
	}
	newOnly, err := NewEncrypterFromHex(newKey)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := newOnly.Decrypt(after); err != nil || got != "new-mailbox-password" {
		t.Fatal("new writes must use only the primary key")
	}
	if _, err := newOnly.Decrypt(before); err == nil {
		t.Fatal("raw-key constructors must not inherit instance read keys")
	}
	if _, err := original.Decrypt(after); err == nil {
		t.Fatal("new writes must not remain sealed with the original key")
	}
}

func TestCredentialReadKeyConfiguration(t *testing.T) {
	t.Setenv("CREDENTIALS_ENCRYPTION_KEY", "")
	t.Setenv("CREDENTIALS_ENCRYPTION_READ_KEYS", "[]")
	if got, err := FromEnv(); err != nil || got != nil {
		t.Fatal("empty self-host defaults must remain supported")
	}
	t.Setenv("CREDENTIALS_ENCRYPTION_READ_KEYS", `["not-a-valid-key"]`)
	if _, err := FromEnv(); err == nil {
		t.Fatal("read keys require a primary key")
	}
	t.Setenv("CREDENTIALS_ENCRYPTION_KEY", strings.Repeat("11", 32))
	if _, err := FromEnv(); err == nil || strings.Contains(err.Error(), "not-a-valid-key") {
		t.Fatal("invalid read keys must fail without disclosing them")
	}
	enc, err := NewEncrypterFromHex(strings.Repeat("11", 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enc.DecryptEncrypted(&Encrypted{Nonce: "00", Ciphertext: "00"}); err == nil {
		t.Fatal("malformed ciphertext must return an error")
	}
}

func TestResealMovesReadKeyValuesToThePrimary(t *testing.T) {
	oldKey, newKey := strings.Repeat("11", 32), strings.Repeat("22", 32)
	original, err := NewEncrypterFromHex(oldKey)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := original.Encrypt("mailbox-password")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CREDENTIALS_ENCRYPTION_KEY", newKey)
	reads, _ := json.Marshal([]string{oldKey})
	t.Setenv("CREDENTIALS_ENCRYPTION_READ_KEYS", string(reads))
	rotated, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	resealed, changed, err := rotated.Reseal(legacy)
	if err != nil || !changed {
		t.Fatalf("a read-key value must be resealed: changed=%v err=%v", changed, err)
	}
	newOnly, _ := NewEncrypterFromHex(newKey)
	if got, err := newOnly.Decrypt(resealed); err != nil || got != "mailbox-password" {
		t.Fatal("a resealed value must open under the primary alone")
	}
	if _, changed, err := rotated.Reseal(resealed); err != nil || changed {
		t.Fatal("a value already under the primary must be left alone")
	}
	stranger, _ := NewEncrypterFromHex(strings.Repeat("33", 32))
	foreign, _ := stranger.Encrypt("other-instance")
	if _, _, err := rotated.Reseal(foreign); err != ErrUnreadable {
		t.Fatal("a value no configured key opens must be reported unreadable")
	}
	if !LooksSealed(legacy) || LooksSealed("ya29.a0Af-token") || LooksSealed("abcd") {
		t.Fatal("LooksSealed must recognise only the sealed hex format")
	}
}

func TestInvalidKeyErrorDoesNotQuoteKeyMaterial(t *testing.T) {
	_, err := NewEncrypterFromHex(strings.Repeat("a", 63) + "z")
	if err == nil || strings.Contains(err.Error(), "z") {
		t.Fatalf("an invalid key must be refused without quoting it, got %v", err)
	}
}
