package tasks

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/pkg/tmplfuncs"
)

const markupName = `Tom & "Jerry" <b>'s`

func htmlContact(name string) models.Contact {
	return models.Contact{FirstName: name, Company: "Acme", CustomFields: map[string]string{
		"website":   "https://acme.com/?a=1&b=2",
		"evil":      "javascript:alert(1)",
		"Evil2":     " JaVaScRiPt:alert(1)",
		"job":       "Head <of> Sales",
		"job title": "VP & CTO",
	}}
}

func TestRenderHTMLTemplateEscapesValuesInContext(t *testing.T) {
	sender := TemplateContext{
		Sender:          templateContext(&models.Email{SignatureHTML: "<b>Ana</b>"}, "").Sender,
		UnsubscribeLink: "https://example.com/unsubscribe/tok_123",
	}
	cases := []struct {
		name, tmpl, first, want string
	}{
		{"plain name", `<p>Hi {{.FirstName}},</p>`, "Ann", `<p>Hi Ann,</p>`},
		{"markup in a name", `<p>Hi {{.FirstName}},</p>`, markupName, `<p>Hi Tom &amp; &#34;Jerry&#34; &lt;b&gt;&#39;s,</p>`},
		{"markup in an attribute", `<img alt="{{.FirstName}}">`, markupName, `<img alt="Tom &amp; &#34;Jerry&#34; &lt;b&gt;&#39;s">`},
		{"custom field", `<p>{{.job}} at {{.Company}}</p>`, "Ann", `<p>Head &lt;of&gt; Sales at Acme</p>`},
		{"spaced custom field", `<p>{{.job title}}</p>`, "Ann", `<p>VP &amp; CTO</p>`},
		{"href keeps a web link", `<a href="{{.website}}">site</a>`, "Ann", `<a href="https://acme.com/?a=1&amp;b=2">site</a>`},
		{"href refuses a script link", `<a href="{{.evil}}">x</a>`, "Ann", `<a href="#ZgotmplZ">x</a>`},
		{"href refuses a padded script link", `<a href="{{.Evil2}}">x</a>`, "Ann", `<a href="#ZgotmplZ">x</a>`},
		{"value in a query", `<a href="https://acme.com/?n={{.FirstName}}">x</a>`, "Ann Lee", `<a href="https://acme.com/?n=Ann%20Lee">x</a>`},
		{"unsubscribe link", `<a href="{{.UnsubscribeLink}}">Unsubscribe</a> {{.UnsubscribeLink}}`, "Ann", `<a href="https://example.com/unsubscribe/tok_123">Unsubscribe</a> https://example.com/unsubscribe/tok_123`},
		{"signature stays markup", `<p>{{.FirstName}}</p>{{.Sender.SignatureHTML}}`, "Ann", `<p>Ann</p><b>Ana</b>`},
		{"missing key", `<p>[{{.nothing}}]</p>`, "Ann", `<p>[]</p>`},
		{"conditional", `{{if eq .FirstName "Tom & \"Jerry\" <b>'s"}}<p>match</p>{{end}}`, markupName, `<p>match</p>`},
		{"outlook comments", `<!--[if mso]><table><tr><td><![endif]--><p>{{.FirstName}}</p><!--[if mso]></td></tr></table><![endif]-->`, "Ann",
			`<!--[if mso]><table><tr><td><![endif]--><p>Ann</p><!--[if mso]></td></tr></table><![endif]-->`},
		{"action inside a comment", `<!--[if mso]><v:roundrect href="{{.evil}}"><![endif]--><p>{{.FirstName}}</p>`, "Ann",
			`<!--[if mso]><v:roundrect href="#ZgotmplZ"><![endif]--><p>Ann</p>`},
		{"no actions is untouched", `<p>1 < 2 &amp; a+b</p><!-- note -->`, "Ann", `<p>1 < 2 &amp; a+b</p><!-- note -->`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RenderHTMLTemplateWith(tc.tmpl, htmlContact(tc.first), sender); got != tc.want {
				t.Fatalf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestRenderHTMLTemplateFallbackEscapes(t *testing.T) {
	cases := []struct{ name, tmpl, want string }{
		{"text", `{{if .Company}}<p>{{.FirstName}}</p>`, `{{if .Company}}<p>Tom &amp; &#34;Jerry&#34; &lt;b&gt;&#39;s</p>`},
		{"attribute", `{{if .Company}}<p title="{{.FirstName}}">`, `{{if .Company}}<p title="Tom &amp; &#34;Jerry&#34; &lt;b&gt;&#39;s">`},
		{"script link", `{{if .Company}}<a href="{{.evil}}">x</a>`, `{{if .Company}}<a href="#ZgotmplZ">x</a>`},
		{"web link", `{{if .Company}}<a href='{{.website}}'>x</a>`, `{{if .Company}}<a href='https://acme.com/?a=1&amp;b=2'>x</a>`},
		{"split link", `{{if .Company}}<a href="{{.FirstName}}{{.evil}}">x</a>`, `{{if .Company}}<a href="Tom &amp; &#34;Jerry&#34; &lt;b&gt;&#39;sjavascript%3Aalert%281%29">x</a>`},
		{"event handler", `{{if .Company}}<p onclick="{{.FirstName}}">`, `{{if .Company}}<p onclick="">`},
		{"inside a tag", `{{if .Company}}<p {{.FirstName}}>`, `{{if .Company}}<p >`},
		{"script", `{{if .Company}}<script>var n = "{{.FirstName}}"</script>`, `{{if .Company}}<script>var n = ""</script>`},
		{"after a comment", `{{if .Company}}<!-- <a href=" --><p>{{.FirstName}}</p>`, `{{if .Company}}<!-- <a href=" --><p>Tom &amp; &#34;Jerry&#34; &lt;b&gt;&#39;s</p>`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RenderHTMLTemplateWith(tc.tmpl, htmlContact(markupName), TemplateContext{}); got != tc.want {
				t.Fatalf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestRenderTemplatePlainPartIsNotEscaped(t *testing.T) {
	if got := RenderTemplateWith(`Hi {{.FirstName}}, {{.website}}`, htmlContact(markupName), TemplateContext{}); got != `Hi `+markupName+`, https://acme.com/?a=1&b=2` {
		t.Fatalf("plain text was altered: %q", got)
	}
}

func TestRenderHTMLTemplateWithSpintax(t *testing.T) {
	got := expandSpintax(RenderHTMLTemplateWith(`<p>{Hi|Hi} {{.FirstName}}</p>`, htmlContact("<i>{a|a}</i>"), TemplateContext{}))
	if got != `<p>Hi &lt;i&gt;a&lt;/i&gt;</p>` {
		t.Fatalf("got %q", got)
	}
}

func TestRenderHTMLTemplateThenTrackLinks(t *testing.T) {
	body := RenderHTMLTemplateWith(`<a href="{{.website}}">site</a> <a href="{{.evil}}">x</a>`, htmlContact("Ann"), TemplateContext{})
	_, links := WrapLinksForTracking(body, uuid.New(), uuid.New(), "track.example.com")
	if len(links) != 1 || links[0].Destination != "https://acme.com/?a=1&b=2" {
		t.Fatalf("tracked %+v from %q", links, body)
	}
}

func TestRenderHTMLTemplateThenLinkifyUnsubscribe(t *testing.T) {
	link := "https://example.com/unsubscribe/tok_123"
	body := RenderHTMLTemplateWith(`<p>Opt out: {{.UnsubscribeLink}}</p>`, htmlContact("Ann"), TemplateContext{UnsubscribeLink: link})
	if got := linkifyUnsubscribeURL(body, link, "Unsubscribe"); got != `<p>Opt out: <a href="`+link+`">Unsubscribe</a></p>` {
		t.Fatalf("got %q", got)
	}
}

func TestRenderTemplateOutputCapHoldsOnFallback(t *testing.T) {
	big := strings.Repeat("x", tmplfuncs.MaxOutput/4)
	contact := models.Contact{CustomFields: map[string]string{"big": big}}
	for _, tmpl := range []string{
		strings.Repeat("{{.big}}", 10),
		"{{if .big}}" + strings.Repeat("{{.big}}", 10),
	} {
		for name, render := range map[string]func(string, models.Contact, TemplateContext) string{
			"text": RenderTemplateWith, "html": RenderHTMLTemplateWith,
		} {
			out := render(tmpl, contact, TemplateContext{})
			if len(out) > tmplfuncs.MaxOutput+len(tmpl) {
				t.Fatalf("%s render wrote %d bytes", name, len(out))
			}
			if !strings.Contains(out, big) {
				t.Fatalf("%s render dropped every value", name)
			}
		}
	}
}

func TestSubstituteAIVariablesEscapesHTMLOnly(t *testing.T) {
	span := `<span data-ai-var="a1" data-ai-config="e30=">[[ai:a1]]</span>`
	subject, bodyHTML, bodyPlain := substituteAIVariables("Re [[ai:a1]]", "<p>"+span+"</p>", "Hi [[ai:a1]]", map[string]string{"a1": `<img src=x onerror=alert(1)> & co`})
	if bodyHTML != `<p>&lt;img src=x onerror=alert(1)&gt; &amp; co</p>` {
		t.Fatalf("html: %q", bodyHTML)
	}
	if bodyPlain != `Hi <img src=x onerror=alert(1)> & co` || subject != `Re <img src=x onerror=alert(1)> & co` {
		t.Fatalf("plain %q subject %q", bodyPlain, subject)
	}
}
