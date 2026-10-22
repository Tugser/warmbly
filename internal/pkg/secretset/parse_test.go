package secretset

import (
	"strings"
	"testing"
)

func TestParseSecretSets(t *testing.T) {
	for _, raw := range []string{"", "[]", `["one","two"]`} {
		if _, err := Parse(raw); err != nil {
			t.Fatalf("valid list: %v", err)
		}
	}
	for _, raw := range []string{`null`, `"private-input"`, `["private-input",3]`, `[""]`, `["a","b","c","d","e"]`} {
		if _, err := Parse(raw); err == nil || strings.Contains(err.Error(), "private-input") {
			t.Fatal("invalid lists must fail without returning their contents")
		}
	}
}
