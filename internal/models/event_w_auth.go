package models

import (
	"time"

	"github.com/google/uuid"
)

type AuthType int

const (
	AuthPlain AuthType = iota
	AuthOAuth2
)

type JobEventTokenUpdate struct {
	UserID       uuid.UUID `json:"user_id" avro:"user_id"`
	EmailID      uuid.UUID `json:"email_id" avro:"email_id"`
	AccessToken  string    `json:"access_token" avro:"access_token"`
	RefreshToken string    `json:"refresh_token" avro:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at" avro:"expires_at"`
	// SealedTokens holds the refreshed token sealed under the organization's
	// data key, and the plaintext fields are then empty.
	SealedTokens string `json:"sealed_tokens,omitempty" avro:"sealed_tokens"`
}
