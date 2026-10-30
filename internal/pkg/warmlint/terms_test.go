package warmlint

import (
	"slices"
	"testing"
)

func TestRewordableNamesWhatTheScoreFlags(t *testing.T) {
	text := "One cheap check: send a Free sample first?! See https://example.com/free"
	terms, stacked := Rewordable(text)
	if !slices.Equal(terms, []string{"cheap", "free"}) || !stacked {
		t.Fatalf("Rewordable = %v, %v", terms, stacked)
	}
	res := Score("", "", text)
	if !slices.ContainsFunc(res.Issues, func(i Issue) bool { return i.Code == "spam_trigger_terms" }) {
		t.Fatal("Score does not flag the terms Rewordable names")
	}
	if terms, stacked := Rewordable("A quick check before the first batch."); len(terms) != 0 || stacked {
		t.Fatalf("clean copy flagged: %v, %v", terms, stacked)
	}
}

func TestTermsCoversEveryCountedTerm(t *testing.T) {
	all := Terms()
	for w := range triggerWords {
		if !slices.Contains(all, w) {
			t.Fatalf("Terms misses %q", w)
		}
	}
	for _, p := range triggerPhrases {
		if !slices.Contains(all, p) {
			t.Fatalf("Terms misses %q", p)
		}
	}
}
