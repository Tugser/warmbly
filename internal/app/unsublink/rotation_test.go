package unsublink

import (
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestReadKeysPreserveIssuedLinksWithinCutoff(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	until := now.Add(Validity)
	previous := New("previous-purpose-secret", "https://example.invalid")
	current := NewWithReadKeys("current-purpose-secret", "https://example.invalid", []string{"previous-purpose-secret"}, until)
	issued := previous.Token(uuid.New(), uuid.New(), uuid.New(), now.Add(-time.Hour))
	if _, err := current.Verify(issued, now); err != nil {
		t.Fatal("previously issued links must remain usable")
	}
	beyond := previous.Token(uuid.New(), uuid.New(), uuid.New(), now.Add(time.Hour))
	if _, err := current.Verify(beyond, now); err != ErrInvalid {
		t.Fatal("previous keys must not extend the configured window")
	}
	if _, err := current.Verify(issued, until); err != ErrInvalid {
		t.Fatal("read keys must expire at the cutoff")
	}
	fresh := current.Token(uuid.New(), uuid.New(), uuid.New(), now)
	if _, err := New("current-purpose-secret", "https://example.invalid").Verify(fresh, now); err != nil {
		t.Fatal("new links must use the current key")
	}
	if _, err := previous.Verify(fresh, now); err != ErrInvalid {
		t.Fatal("new links must not use a read key")
	}
}
