package tasks

import (
	"strings"
	"testing"

	"github.com/warmbly/warmbly/internal/models"
)

// Contact data is data: markup is escaped and a template action inside a value is never evaluated.
func TestContactValuesCannotInjectMarkupOrTemplateActions(t *testing.T) {
	contact := models.Contact{
		FirstName: `<script>alert(1)</script>`,
		Email:     "ann@example.com",
		Company:   `" onmouseover="alert(1)`,
		CustomFields: map[string]string{
			"note": `{{.Email}}{{printf "%s" .Email}}{{range .}}x{{end}}`,
		},
	}
	for _, tc := range []struct{ name, tmpl string }{
		{"compiled", `<p>{{.FirstName}}</p><a title="{{.Company}}">x</a><p>{{.note}}</p>`},
		{"fallback", `{{if .Company}}<p>{{.FirstName}}</p><a title="{{.Company}}">x</a><p>{{.note}}</p>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := RenderHTMLTemplateWith(tc.tmpl, contact, TemplateContext{})
			for _, bad := range []string{"<script>", `" onmouseover="`, "ann@example.com"} {
				if strings.Contains(out, bad) {
					t.Fatalf("value escaped its context (%q): %s", bad, out)
				}
			}
			if !strings.Contains(out, "&lt;script&gt;") || !strings.Contains(out, "{{.Email}}") {
				t.Fatalf("value was not kept as escaped text: %s", out)
			}
		})
	}
	if out := RenderTemplateWith(`Hi {{.note}}`, contact, TemplateContext{}); strings.Contains(out, "ann@example.com") {
		t.Fatalf("plain part evaluated an action inside a value: %s", out)
	}
}
