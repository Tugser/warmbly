package fleetnode

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Issuing a token revokes the one before it; nodes it enrolled are untouched.
func TestIssuingAJoinTokenRevokesThePreviousOne(t *testing.T) {
	ctx := context.Background()
	s := &Service{settings: &memoryJoinToken{}}
	old, _, err := s.IssueJoinToken(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	fresh, _, err := s.IssueJoinToken(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyJoinToken(ctx, old); !errors.Is(err, ErrBadToken) {
		t.Fatalf("replaced token: %v, want ErrBadToken", err)
	}
	if err := s.VerifyJoinToken(ctx, fresh); err != nil {
		t.Fatalf("current token: %v", err)
	}
}
