package a2ui

import (
	"strings"
	"testing"
)

// A Link opens an http(s) address as a whole: nothing else, nothing a
// reader could misread.
func TestLinkURLOK(t *testing.T) {
	for _, u := range []string{
		"https://example.org",
		"http://example.org/projets/terrasse",
		"https://example.org:8443/a/b?tab=checks#run-2",
		"HTTPS://example.org/a",
		"https://192.0.2.10/merge/12",
	} {
		if !LinkURLOK(u) {
			t.Errorf("%q refused", u)
		}
	}
	for _, u := range []string{
		"",
		"javascript:alert(1)",
		"data:text/html,<b>x</b>",
		"mailto:sam@example.org",
		"ftp://example.org/a",
		"file:///etc/passwd",
		"//example.org/a",
		"/espace",
		"example.org",
		"https:example.org",
		"https://",
		"https://sam:secret@example.org/",
		"https://sam@example.org/",
		"https://example.org/a b",
		"https://example.org/\ta",
		"https://example.org\\@evil.example.org/",
		" https://example.org",
		"https://example.org/\u0085",
		"https://example.org/" + strings.Repeat("a", LinkURLMax),
	} {
		if LinkURLOK(u) {
			t.Errorf("%q accepted", u)
		}
	}
}

// The schema refuses a computed address; CheckLinks a literal that is not
// one a Link may open; a binding is checked where its value is.
func TestLinkValidation(t *testing.T) {
	ok := surfaceOf(
		`{"id":"root","component":"Column","children":["home","list"]}`,
		`{"id":"home","component":"Link","label":"Ouvrir","url":"https://example.org/projets"}`,
		`{"id":"list","component":"List","children":{"path":"/changes","componentId":"open"}}`,
		`{"id":"open","component":"Link","label":{"@path":"title"},"url":{"@path":"url"}}`,
	)
	if errs := ValidateSurface(ok); len(errs) > 0 {
		t.Fatal(errs)
	}
	if errs := CheckLinks(ok.ID, ok.Components); len(errs) > 0 {
		t.Fatal(errs)
	}
	var link Bound
	for _, b := range Eriphe().BoundPaths(ok.Components) {
		if b.Path == "/changes/*/url" {
			link = b
		}
		if b.Link && b.Path != "/changes/*/url" {
			t.Errorf("%+v is not a Link's url", b)
		}
	}
	if !link.Link || link.Component != "open" || link.At != "/3/url/@path" {
		t.Errorf("the template's url: %+v", link)
	}

	built := surfaceOf(`{"id":"root","component":"Link","url":{"@call":"formatString","args":{"value":"https://example.org/?q=${/secret}"}}}`)
	if errs := ValidateSurface(built); len(errs) == 0 || errs[0].Path != "/components/0/url" {
		t.Errorf("an address built by formatString: %v", errs)
	}
	for _, c := range []struct{ name, url string }{
		{"a script", "javascript:alert(1)"},
		{"another scheme", "ftp://example.org/a"},
		{"a password", "https://sam:secret@example.org/"},
		{"no host", "/espace"},
		{"a backslash", `https://example.org\\@evil.example.org/`},
	} {
		s := surfaceOf(`{"id":"root","component":"Link","url":"` + c.url + `"}`)
		if errs := ValidateSurface(s); len(errs) > 0 {
			t.Errorf("%s: the schema takes any text: %v", c.name, errs)
		}
		errs := CheckLinks(s.ID, s.Components)
		if len(errs) != 1 || errs[0].Path != "/components/0/url" || errs[0].Code != CodeValidationFailed || !strings.Contains(errs[0].Message, "http(s)") {
			t.Errorf("%s: %v", c.name, errs)
		}
	}
	empty := surfaceOf(`{"id":"root","component":"Link","url":""}`)
	if errs := ValidateSurface(empty); len(errs) == 0 {
		t.Error("an empty url passed the schema")
	}
	if !IsLink("Link", "url") || IsLink("Link", "label") || IsLink("Image", "url") {
		t.Error("IsLink")
	}
}

// Code shows text as it is; its mode is code or diff, nothing else.
func TestCodeValidation(t *testing.T) {
	for _, s := range []Surface{
		surfaceOf(`{"id":"root","component":"Code","text":"fmt.Println(1)","language":"go"}`),
		surfaceOf(`{"id":"root","component":"Code","mode":"diff","text":{"@path":"/diff"}}`),
	} {
		if errs := ValidateSurface(s); len(errs) > 0 {
			t.Errorf("%s: %v", s.Components, errs)
		}
	}
	for _, c := range []struct {
		name, comp, path, words string
	}{
		{"another mode", `{"id":"root","component":"Code","mode":"patch","text":"a"}`, "/components/0/mode", `'code', 'diff'`},
		{"no text", `{"id":"root","component":"Code","mode":"diff"}`, "/components/0", "missing property 'text'"},
		{"a bound language", `{"id":"root","component":"Code","text":"a","language":{"@path":"/lang"}}`, "/components/0/language", "want string"},
	} {
		errs := ValidateSurface(surfaceOf(c.comp))
		if len(errs) != 1 || errs[0].Path != c.path || !strings.Contains(errs[0].Message, c.words) {
			t.Errorf("%s: %v", c.name, errs)
		}
	}
}
