package slackapp

import (
	"context"
	"encoding/hex"
	"encoding/json"

	"github.com/google/uuid"
)

// heldAskKey names a question held under a link code; the code is hashed so
// the key never reveals it.
func heldAskKey(orgID uuid.UUID, code string) string {
	return "slack:held:" + orgID.String() + ":" + hex.EncodeToString(hashLinkCode(code))
}

// holdAsk keeps a question until its author redeems code, sealed with the
// org's DEK because it is message content. False when it cannot be kept.
func (s *Service) holdAsk(ctx context.Context, orgID uuid.UUID, code string, q *ask) bool {
	if s.cipher == nil {
		return false
	}
	raw, err := json.Marshal(q)
	if err != nil {
		return false
	}
	c, err := s.cipher.Cipher(ctx, orgID)
	if err != nil {
		return false
	}
	sealed, err := c.Encrypt(ctx, string(raw))
	if err != nil {
		return false
	}
	s.guard.put(ctx, heldAskKey(orgID, code), sealed, linkCodeTTL)
	return true
}

// takeAsk returns and forgets the question held under code, if any.
func (s *Service) takeAsk(ctx context.Context, orgID uuid.UUID, code string) *ask {
	if s.cipher == nil {
		return nil
	}
	sealed := s.guard.take(ctx, heldAskKey(orgID, code))
	if sealed == "" {
		return nil
	}
	c, err := s.cipher.Cipher(ctx, orgID)
	if err != nil {
		return nil
	}
	raw, err := c.Decrypt(ctx, sealed)
	if err != nil {
		return nil
	}
	var q ask
	if json.Unmarshal([]byte(raw), &q) != nil {
		return nil
	}
	return &q
}
