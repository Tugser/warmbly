package integration

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/warmbly/warmbly/internal/app/webhook"
	"github.com/warmbly/warmbly/internal/pkg/tmplfuncs"
)

// Condition expressions and action templates compile through tmplfuncs.Compile:
// the shared helper set (arithmetic, coercing numeric comparison, string
// helpers, default/fallback) plus the built-in eq/ne/lt/le/gt/ge and and/or/not,
// with the same resource limits as campaign email bodies.
var exprTmplCache tmplfuncs.Cache

// prepExpr lets a user write either a full template (`{{if gt .x 1}}y{{end}}`)
// or a bare boolean pipeline (`gt .x 1`), which we wrap in an {{if}}.
func prepExpr(expr string) string {
	expr = strings.TrimSpace(expr)
	if !strings.Contains(expr, "{{") {
		return "{{if " + expr + "}}true{{end}}"
	}
	return expr
}

func compileExpr(expr string) *template.Template {
	if t, ok := exprTmplCache.Load(expr); ok {
		return t
	}
	t, err := tmplfuncs.Compile("cond", prepExpr(expr))
	if err != nil {
		exprTmplCache.Store(expr, nil)
		return nil
	}
	exprTmplCache.Store(expr, t)
	return t
}

// EvalExpression renders a condition expression against the NATIVE event data
// (numbers stay numbers) and reports whether it is "truthy" — i.e. renders a
// non-empty, non-false value. Any parse/exec failure is a false (a broken
// condition never silently passes).
func EvalExpression(expr string, data map[string]any) bool {
	if strings.TrimSpace(expr) == "" {
		return false
	}
	t := compileExpr(expr)
	if t == nil {
		return false
	}
	out, err := tmplfuncs.Execute(t, data)
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(out)) {
	case "", "false", "0", "no", "off", "<no value>":
		return false
	}
	return true
}

// ValidExpression reports whether a condition expression parses (used on write,
// so a campaign can't be saved with a broken predicate).
func ValidExpression(expr string) error {
	if strings.TrimSpace(expr) == "" {
		return fmt.Errorf("expression is empty")
	}
	_, err := tmplfuncs.Compile("cond", prepExpr(expr))
	return err
}

// Templating for automation/integration action values (message bodies, channels,
// webhook URLs, CRM static field values). Renders the FULL, standard Go
// text/template engine against the NATIVE event-data map — the same data
// conditions evaluate against — so an action value can do everything a Go
// template can, not just substitute a value: conditionals/else, {{range}} over
// lists, {{with}}, nested dotted access ({{.lead.company}}), pipelines, and the
// shared helper funcs, with numbers and booleans keeping their type (so native
// {{if gt .confidence 0.8}} works, no coercion needed). Variables use standard
// dotted field access ({{.contact_email}}); there is no bare-{{key}} shorthand —
// the template is plain Go text/template. Unknown keys render empty. Never
// hard-fails: any parse/exec error falls back to naive {{.key}} substitution.

// tmplCache is bounded, so many distinct template strings cannot grow it
// without limit; past the cap a miss recompiles.
var tmplCache tmplfuncs.Cache

// renderOutboundURL renders a (possibly templated) outbound webhook URL and
// re-validates the result against the SSRF/HTTPS guard. A non-empty input that
// renders to empty is treated as a misconfiguration (error), not a silent skip.
func renderOutboundURL(raw string, data map[string]any) (string, error) {
	url := renderTemplate(raw, data)
	if url == "" {
		return "", fmt.Errorf("webhook url rendered empty")
	}
	if err := webhook.ValidateOutboundURL(url); err != nil {
		return "", fmt.Errorf("rendered webhook url failed validation: %w", err)
	}
	return url, nil
}

// renderTemplate renders tmpl against the native event data map.
func renderTemplate(tmpl string, data map[string]any) string {
	if !strings.Contains(tmpl, "{{") {
		return strings.TrimSpace(tmpl)
	}
	t := compileTemplate(tmpl)
	if t == nil {
		return naiveRenderTemplate(tmpl, data)
	}
	out, err := tmplfuncs.Execute(t, data)
	if err != nil {
		return naiveRenderTemplate(tmpl, data)
	}
	return strings.TrimSpace(stripNoValue(out))
}

// stripNoValue removes the text/template "<no value>" sentinel that
// missingkey=zero emits for an absent key on a map[string]any (its element type
// is interface{}, whose zero value prints as that sentinel). Stripping it keeps
// the documented contract — an unknown placeholder renders empty — instead of
// leaking "<no value>" into a customer-facing Slack/webhook/CRM value.
func stripNoValue(s string) string {
	if !strings.Contains(s, "<no value>") {
		return s
	}
	return strings.ReplaceAll(s, "<no value>", "")
}

func compileTemplate(tmpl string) *template.Template {
	if t, ok := tmplCache.Load(tmpl); ok {
		return t
	}
	t, err := tmplfuncs.Compile("action", tmpl)
	if err != nil {
		tmplCache.Store(tmpl, nil)
		return nil
	}
	tmplCache.Store(tmpl, t)
	return t
}

// naiveRenderTemplate is a literal {{.key}} substitution (a leading dot is
// optional here), used as a safe fallback when a template can't compile/execute
// so a single malformed block never blanks the whole value. One left-to-right
// pass: a substituted value is never rescanned, and values share MaxOutput.
func naiveRenderTemplate(tmpl string, data map[string]any) string {
	var b strings.Builder
	budget := tmplfuncs.MaxOutput
	rest := tmpl
	for {
		start := strings.Index(rest, "{{")
		if start < 0 {
			break
		}
		end := strings.Index(rest[start:], "}}")
		if end < 0 {
			break
		}
		end += start
		key := strings.TrimPrefix(strings.TrimSpace(rest[start+2:end]), ".")
		value := stringFromMap(data, key)
		if len(value) > budget {
			value = ""
		}
		budget -= len(value)
		b.WriteString(rest[:start])
		b.WriteString(value)
		rest = rest[end+2:]
	}
	b.WriteString(rest)
	return strings.TrimSpace(b.String())
}
