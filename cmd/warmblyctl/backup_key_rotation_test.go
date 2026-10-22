package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBackupKeyListsRoundTrip(t *testing.T) {
	root := t.TempDir()
	dump := filepath.Join(root, "database.sql")
	if err := os.WriteFile(dump, []byte("SELECT 1;\n"), 0600); err != nil {
		t.Fatal(err)
	}
	keys := map[string]string{"CREDENTIALS_ENCRYPTION_KEY": "fixture-primary", "CREDENTIALS_ENCRYPTION_READ_KEYS": `["fixture-reader"]`, "TWOFA_SECRET": "fixture-mfa", "TWOFA_READ_SECRETS": `["reader,with,commas"]`, "UNSUBSCRIBE_SIGNING_READ_UNTIL": "2027-10-22T00:00:00Z", "NODE_BROKER_PREVIOUS_TOKENS": `["fixture-broker"]`}
	bundle := filepath.Join(root, "bundle.tar.gz")
	if err := writeBundle(bundle, manifest{Version: 1}, dump, nil, keys); err != nil {
		t.Fatal(err)
	}
	restored, err := readKeys(bundle)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range keys {
		if restored[key] != want {
			t.Fatalf("backup must preserve %s exactly", key)
		}
	}
}
