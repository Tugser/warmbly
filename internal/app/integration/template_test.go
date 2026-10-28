package integration

import "testing"

func TestRenderTemplateRangesOverData(t *testing.T) {
	data := map[string]any{"lead": map[string]any{"tags": []any{"a", "b"}}}
	if got := renderTemplate(`{{range .lead.tags}}{{.}};{{end}}`, data); got != "a;b;" {
		t.Fatalf("got %q", got)
	}
}

func TestRenderTemplateRefusesRangeOverNumber(t *testing.T) {
	got := renderTemplate(`{{range 100000000000}}x{{end}}`, map[string]any{})
	if got != "x" {
		t.Fatalf("got %d bytes, want the naive fallback", len(got))
	}
	if EvalExpression(`{{range 100000000000}}x{{end}}`, map[string]any{}) {
		t.Fatal("a range over a number evaluated true")
	}
	if ValidExpression(`{{range 100000000000}}x{{end}}`) == nil {
		t.Fatal("ValidExpression accepted a range over a number")
	}
}

func TestNaiveRenderNeverExpandsSubstitutedValues(t *testing.T) {
	data := map[string]any{"a": "{{.a}}{{.a}}", "b": "B"}
	if got := naiveRenderTemplate(`{{if}} {{.a}}|{{b}}`, data); got != "{{.a}}{{.a}}|B" {
		t.Fatalf("got %q", got)
	}
}
