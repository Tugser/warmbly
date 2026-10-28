package tmplfuncs

import (
	"errors"
	"strings"
	"testing"
)

func TestBuiltinsAreBounded(t *testing.T) {
	big := strings.Repeat("x", MaxOutput/2+1)
	data := map[string]any{
		"name": "Ann", "big": big, "list": []string{big, big, big},
		"fn": func() string { return "called" },
	}
	for _, src := range []string{
		`{{call .fn}}`,
		`{{printf "%2000000s" .name}}`,
		`{{printf "%.2000000f" 1.5}}`,
		`{{printf "%*s" 2000000 .name}}`,
		`{{printf "%[1]s%[1]s%[1]s" .big}}`,
		`{{printf "%s%s" .big .big}}`,
		`{{print .big .big}}`,
		`{{println .big .big}}`,
		`{{html .big .big}}`,
		`{{js .big .big}}`,
		`{{urlquery .big .big}}`,
		`{{join .list ","}}`,
	} {
		for kind, compile := range map[string]func(string, string) (Executor, error){
			"text": func(n, s string) (Executor, error) { return Compile(n, s) },
			"html": func(n, s string) (Executor, error) { return CompileHTML(n, s) },
		} {
			tmpl, err := compile("t", src)
			if err != nil {
				continue
			}
			if out, err := Execute(tmpl, data); err == nil {
				t.Errorf("%s %s rendered %d bytes, want a refusal", kind, src, len(out))
			}
		}
	}
}

func TestBuiltinsStillRender(t *testing.T) {
	data := map[string]any{"name": "Ann & Co", "n": 3, "list": []string{"a", "b"}}
	for src, want := range map[string]string{
		`{{printf "%s-%03d" .name .n}}`:          "Ann & Co-003",
		`{{print .name .n}}`:                     "Ann & Co3",
		`{{html .name}}`:                         "Ann &amp; Co",
		`{{urlquery .name}}`:                     "Ann+%26+Co",
		`{{join .list ", "}}`:                    "a, b",
		`{{.name | printf "%q"}}`:                `"Ann & Co"`,
		`{{range .list}}{{.}}{{end}}`:            "ab",
		`{{range $i, $v := .list}}{{$i}}{{end}}`: "01",
	} {
		tmpl, err := Compile("t", src)
		if err != nil {
			t.Fatalf("Compile(%q): %v", src, err)
		}
		if out, err := Execute(tmpl, data); err != nil || out != want {
			t.Errorf("%s = %q, %v; want %q", src, out, err, want)
		}
	}
}

func TestExecuteBoundsRangeSteps(t *testing.T) {
	const src = `{{range .n}}{{range $.n}}{{end}}{{end}}`
	text, err := Compile("t", src)
	if err != nil {
		t.Fatal(err)
	}
	html, err := CompileHTML("t", src)
	if err != nil {
		t.Fatal(err)
	}
	for _, tmpl := range []Executor{text, html} {
		if _, err := Execute(tmpl, map[string]any{"n": 100000}); !errors.Is(err, ErrStepLimit) {
			t.Fatalf("err = %v, want ErrStepLimit", err)
		}
	}
}

func TestCompileRefusesTooManyVariablesAndArgs(t *testing.T) {
	vars := strings.Repeat(`{{$a := 1}}`, MaxVariables+1)
	args := `{{print` + strings.Repeat(" 1", MaxArgs+1) + `}}`
	for _, src := range []string{vars, args} {
		if _, err := Compile("t", src); err == nil {
			t.Errorf("Compile accepted %.40s...", src)
		}
		if _, err := CompileHTML("t", src); err == nil {
			t.Errorf("CompileHTML accepted %.40s...", src)
		}
	}
}

func TestCompileHTMLEscapesInContext(t *testing.T) {
	tmpl, err := CompileHTML("t", `<a href="{{.u}}" title="{{.n}}">{{.n}}</a>`)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Execute(tmpl, map[string]any{"u": "javascript:alert(1)", "n": `<b>"x"</b>`})
	if err != nil || out != `<a href="#ZgotmplZ" title="&lt;b&gt;&#34;x&#34;&lt;/b&gt;">&lt;b&gt;&#34;x&#34;&lt;/b&gt;</a>` {
		t.Fatalf("got %q, %v", out, err)
	}
}
