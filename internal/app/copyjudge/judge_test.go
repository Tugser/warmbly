package copyjudge

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/warmbly/warmbly/internal/pkg/typesafe"
)

// fakeAsker records the state it was handed and answers from a canned
// response, so the policy is tested against numbers rather than a network.
type fakeAsker struct {
	calls int
	state state
	resp  *typesafe.Response
	err   error
}

func (f *fakeAsker) Ask(_ context.Context, st any, _ map[string]typesafe.Question) (*typesafe.Response, error) {
	f.calls++
	f.state = st.(state)
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

func response(readsAs, readsConf, personal, personalConf float64, ask string, askConf, spam float64) *typesafe.Response {
	r := &typesafe.Response{Model: typesafe.Model, Answers: map[string]typesafe.Answer{
		qReadsAs:         {Type: typesafe.QuestionScore, Score: readsAs, Confidence: readsConf},
		qPersonalization: {Type: typesafe.QuestionScore, Score: personal, Confidence: personalConf},
		qAsk:             {Type: typesafe.QuestionChoice, Choice: ask, Confidence: askConf},
		qSpamClaim:       {Type: typesafe.QuestionNoul, Noul: spam},
	}}
	r.Usage.InputTokens = 321
	return r
}

func TestJudgeReadsEveryAnswerIntoTheVerdict(t *testing.T) {
	f := &fakeAsker{resp: response(2, 0.9, 1, 0.85, AskNone, 0.8, 0.1)}
	v, err := Judge(context.Background(), f, "  quick question ", "Hi Sam, saw your post.")
	if err != nil {
		t.Fatal(err)
	}
	if f.calls != 1 {
		t.Fatalf("expected one call, got %d", f.calls)
	}
	if v.ReadsAs != 1 || v.Personalization != 1 {
		t.Errorf("scores not normalized to the rubric: reads_as %.2f personalization %.2f", v.ReadsAs, v.Personalization)
	}
	if v.Ask != AskNone || v.SpamClaim != 0.1 {
		t.Errorf("ask %q spam %.2f", v.Ask, v.SpamClaim)
	}
	if v.Confidence != 0.8 {
		t.Errorf("confidence should be the lowest of the three, got %.2f", v.Confidence)
	}
	if v.Model != typesafe.Model || v.InputTokens != 321 {
		t.Errorf("usage not carried: %q %d", v.Model, v.InputTokens)
	}
	if f.state.Subject != "quick question" {
		t.Errorf("subject not trimmed: %q", f.state.Subject)
	}
}

func TestJudgeRefusesEmptyCopyWithoutACall(t *testing.T) {
	f := &fakeAsker{resp: response(0, 1, 0, 1, AskOneClear, 1, 0)}
	if _, err := Judge(context.Background(), f, "  ", "<p> </p>"); !errors.Is(err, ErrNoContent) {
		t.Fatalf("expected ErrNoContent, got %v", err)
	}
	if f.calls != 0 {
		t.Error("an empty body was sent to the model")
	}
}

func TestJudgeFlattensHTMLAndCapsTheBody(t *testing.T) {
	f := &fakeAsker{resp: response(0, 1, 0, 1, AskOneClear, 1, 0)}
	long := strings.Repeat("word ", BodyLimit)
	body := "<html><head><style>p{color:red}</style></head><body><p>Hi Sam,</p><p>" + long + "</p></body></html>"
	if _, err := Judge(context.Background(), f, "hello", body); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.state.Body, "<p>") || strings.Contains(f.state.Body, "color:red") {
		t.Errorf("markup reached the model: %q", f.state.Body[:60])
	}
	if !strings.HasPrefix(f.state.Body, "Hi Sam,") {
		t.Errorf("text lost: %q", f.state.Body[:40])
	}
	if n := len([]rune(f.state.Body)); n > BodyLimit {
		t.Errorf("body sent at %d runes, cap is %d", n, BodyLimit)
	}
}

func TestJudgePropagatesAskerErrors(t *testing.T) {
	f := &fakeAsker{err: errors.New("boom")}
	if _, err := Judge(context.Background(), f, "s", "b"); err == nil {
		t.Fatal("expected the asker's error")
	}
}

func TestJudgeRejectsAMissingOrUnknownAnswer(t *testing.T) {
	r := response(0, 1, 0, 1, AskOneClear, 1, 0)
	delete(r.Answers, qSpamClaim)
	if _, err := Judge(context.Background(), &fakeAsker{resp: r}, "s", "b"); err == nil {
		t.Error("a response missing an answer was accepted")
	}
	if _, err := Judge(context.Background(), &fakeAsker{resp: response(0, 1, 0, 1, "maybe", 1, 0)}, "s", "b"); err == nil {
		t.Error("an ask outside the option set was accepted")
	}
}

func TestReadsAsBulkNeedsConfidenceUnlessAClaimIsMade(t *testing.T) {
	cases := []struct {
		name string
		v    Verdict
		want bool
	}{
		{"confident bulk and generic", Verdict{ReadsAs: 1, Personalization: 1, Confidence: 0.9}, true},
		{"at the threshold", Verdict{ReadsAs: BulkAt, Personalization: BulkAt, Confidence: ConfFloor}, true},
		{"promotional but relevant", Verdict{ReadsAs: 1, Personalization: 0, Confidence: 0.9}, false},
		{"generic but conversational", Verdict{ReadsAs: 0, Personalization: 1, Confidence: 0.9}, false},
		{"relevance borderline", Verdict{ReadsAs: 1, Personalization: BulkAt - 0.01, Confidence: 0.9}, false},
		{"bulk but unsure", Verdict{ReadsAs: 1, Personalization: 1, Confidence: 0.5}, false},
		{"between", Verdict{ReadsAs: 0.5, Confidence: 0.95}, false},
		{"personal with a spam claim", Verdict{ReadsAs: 0, Confidence: 0.2, SpamClaim: 0.8}, true},
		{"spam claim below its line", Verdict{ReadsAs: 0, Confidence: 0.9, SpamClaim: 0.5}, false},
	}
	for _, tc := range cases {
		if got := tc.v.ReadsAsBulk(); got != tc.want {
			t.Errorf("%s: ReadsAsBulk = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestLacksClearAskRespectsTheFloor(t *testing.T) {
	if !(Verdict{Ask: AskNone, Confidence: 0.9}).LacksClearAsk() {
		t.Error("a confident no_ask should fire")
	}
	if !(Verdict{Ask: AskSeveral, Confidence: 0.7}).LacksClearAsk() {
		t.Error("a confident several_asks should fire")
	}
	if (Verdict{Ask: AskNone, Confidence: 0.4}).LacksClearAsk() {
		t.Error("an unsure answer must not fire")
	}
	if (Verdict{Ask: AskOneClear, Confidence: 1}).LacksClearAsk() {
		t.Error("one clear ask is the good case")
	}
}

func TestSummaryReadsAsASentence(t *testing.T) {
	got := (Verdict{ReadsAs: 0, Ask: AskOneClear, Confidence: 0.9}).Summary()
	if got != "Reads as a personal note with one clear ask." {
		t.Errorf("got %q", got)
	}
	got = (Verdict{ReadsAs: 1, Ask: AskSeveral, Confidence: 0.5, SpamClaim: 0.9}).Summary()
	if !strings.HasPrefix(got, "Probably reads as bulk mail that asks for several things.") || !strings.Contains(got, "spam filter") {
		t.Errorf("got %q", got)
	}
}

func TestContentHashIsStableAcrossTrailingWhitespace(t *testing.T) {
	a := ContentHash("Subject", "Body\n")
	b := ContentHash(" Subject ", "Body")
	if a != b {
		t.Error("trimmed copy should hash the same")
	}
	if a == ContentHash("Subject", "Body.") {
		t.Error("a rewrite should hash differently")
	}
	if len(a) != 64 {
		t.Errorf("expected hex sha256, got %d chars", len(a))
	}
}

func TestBodyPrefersPlainText(t *testing.T) {
	if Body("plain", "<p>html</p>") != "plain" {
		t.Error("plain text should win when present")
	}
	if Body("  ", "<p>html</p>") != "<p>html</p>" {
		t.Error("a blank plain body should fall back to HTML")
	}
}
