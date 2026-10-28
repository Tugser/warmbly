package warmlint

import "sort"

// Terms lists every spam-trigger word and phrase the score counts, sorted, so
// an AI prompt can name exactly what the editor will flag.
func Terms() []string {
	out := make([]string, 0, len(triggerWords)+len(triggerPhrases))
	for w := range triggerWords {
		out = append(out, w)
	}
	out = append(out, triggerPhrases...)
	sort.Strings(out)
	return out
}

// Rewordable reports what in text the score would flag that rewording fixes:
// the distinct spam-trigger terms in it, and whether it stacks punctuation.
func Rewordable(text string) (terms []string, stacked bool) {
	terms, _ = triggerSpans("", text)
	return terms, stackedPunct.MatchString(text)
}
