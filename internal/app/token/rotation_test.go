package token

import (
	"encoding/json"
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestSessionRotationDoesNotAcceptPurposeReadKeys(t *testing.T) {
	previous := &tokenService{AuthSecret: "previous-session-secret-at-least-32-characters"}
	current := &tokenService{AuthSecret: "current-session-secret-at-least-32-characters"}
	now := time.Now()
	readKeys, err := json.Marshal([]string{previous.AuthSecret})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"TWOFA_READ_SECRETS", "UNSUBSCRIBE_SIGNING_READ_SECRETS"} {
		t.Setenv(key, string(readKeys))
	}
	t.Setenv("UNSUBSCRIBE_SIGNING_READ_UNTIL", now.Add(time.Hour).UTC().Format(time.RFC3339))
	for _, purpose := range []string{PurposeAccess, PurposeRefresh, PurposeWebSocket, PurposePasswordReset, PurposeTwoFAPending} {
		encoded, err := previous.GenerateTokenFor(purpose, uuid.New(), uuid.New(), "", "nonce", now, now.Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := current.VerifyTokenFor(purpose, encoded); err == nil {
			t.Fatal("previous session signatures must be refused for every purpose")
		}
	}
}
