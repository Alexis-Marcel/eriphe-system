package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
)

// The workspace's fonts come from eriphe-core itself, beside its stylesheet,
// as fonts: the CSP allows no other source.
func TestEspaceFonts(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	_, css, _ := tw.do("GET", "/static/espace/espace.css", nil, nil)
	urls := regexp.MustCompile(`url\(\./(espace-[a-z-]+\.woff2)\)`).FindAllStringSubmatch(css, -1)
	if len(urls) != 2 {
		t.Fatalf("fonts in the stylesheet: %v", urls)
	}
	for _, u := range urls {
		code, _, h := tw.do("GET", "/static/espace/"+u[1], nil, nil)
		if code != http.StatusOK || h.Get("Content-Type") != "font/woff2" {
			t.Errorf("%s: %d %s", u[1], code, h.Get("Content-Type"))
		}
	}
}

// /espace serves the app's shell to a session only, with the workspace's
// security headers and nothing inline.
func TestEspacePage(t *testing.T) {
	tw := newTestWeb(t)
	csrf := tw.setup()

	code, body, h := tw.do("GET", "/espace", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("GET /espace: %d", code)
	}
	for _, want := range []string{`src="/static/espace/espace.js?v=` + assetVersion() + `"`,
		`href="/static/espace/espace.css?v=` + assetVersion() + `"`, `data-boot="/espace/boot"`, `<html lang="fr">`} {
		if !strings.Contains(body, want) {
			t.Errorf("shell lacks %s:\n%s", want, body)
		}
	}
	csp := h.Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "style-src 'self'") || strings.Contains(csp, "unsafe") {
		t.Errorf("CSP: %q", csp)
	}
	if h.Get("X-Frame-Options") != "DENY" || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Cache-Control") != "no-store" {
		t.Errorf("headers: %v", h)
	}
	// Every script has a source; no style element or attribute.
	for _, m := range regexp.MustCompile(`<script[^>]*>`).FindAllString(body, -1) {
		if !strings.Contains(m, ` src="/static/`) {
			t.Errorf("inline script: %s", m)
		}
	}
	if strings.Contains(body, "<style") || strings.Contains(body, "style=") {
		t.Errorf("inline style in the shell:\n%s", body)
	}

	// The boot: the session's token, the words in the request's language.
	code, body, h = tw.do("GET", "/espace/boot", nil, nil)
	if code != http.StatusOK || !strings.HasPrefix(h.Get("Content-Type"), "application/json") {
		t.Fatalf("GET /espace/boot: %d %s", code, h.Get("Content-Type"))
	}
	var b espaceBoot
	if err := json.Unmarshal([]byte(body), &b); err != nil {
		t.Fatal(err)
	}
	if b.CSRF != csrf || b.Lang != "fr" || b.Name != assistantName() || b.Strings["espace.input.about"] != "Écris à Eriphe, sur %s" {
		t.Errorf("boot: %+v", b)
	}
	if strings.Contains(b.Strings["espace.input"], "{{name}}") {
		t.Errorf("name not put in: %q", b.Strings["espace.input"])
	}

	// The built app is embedded.
	for _, f := range []string{"/static/espace/espace.js", "/static/espace/espace.css"} {
		if code, body, _ := tw.do("GET", f, nil, nil); code != http.StatusOK || len(body) < 100 {
			t.Errorf("GET %s: %d, %d bytes (run npm run build in web/espace)", f, code, len(body))
		}
	}

	// Without a session: to the sign-in, the boot included.
	jar, _ := cookiejar.New(nil)
	tw.hc.Jar = jar
	for _, path := range []string{"/espace", "/espace/boot"} {
		code, _, h := tw.do("GET", path, nil, nil)
		if code != http.StatusSeeOther || h.Get("Location") != "/login" {
			t.Errorf("GET %s without a session: %d %s", path, code, h.Get("Location"))
		}
	}
}

// The catalog of shapes is served to a session, as core holds it.
func TestEspaceCatalog(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	code, body, h := tw.do("GET", "/espace/catalog/v1.json", nil, nil)
	if code != http.StatusOK || !strings.HasPrefix(h.Get("Content-Type"), "application/json") {
		t.Fatalf("GET /espace/catalog/v1.json: %d %s", code, h.Get("Content-Type"))
	}
	if body != string(a2ui.CatalogJSON()) {
		t.Error("the served catalog is not core's")
	}
	var doc struct {
		CatalogID  string         `json:"catalogId"`
		Components map[string]any `json:"components"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil || doc.CatalogID != a2ui.CatalogID || doc.Components["Metric"] == nil {
		t.Errorf("catalog: %v %s %d components", err, doc.CatalogID, len(doc.Components))
	}
	jar, _ := cookiejar.New(nil)
	tw.hc.Jar = jar
	if code, _, h := tw.do("GET", "/espace/catalog/v1.json", nil, nil); code != http.StatusSeeOther || h.Get("Location") != "/login" {
		t.Errorf("without a session: %d %s", code, h.Get("Location"))
	}
}

// A message may link to /espace, and the conversation's top bar does.
func TestEspaceLinked(t *testing.T) {
	if got := link("/espace", "Espace"); !strings.Contains(got, `href="/espace"`) {
		t.Errorf("link: %s", got)
	}
	tw := newTestWeb(t)
	tw.setup()
	if _, body, _ := tw.do("GET", "/settings/account", nil, nil); !strings.Contains(body, `href="/espace"`) {
		t.Error("no link to /espace in the top bar")
	}
}

// The app's words are in both languages, and its dev server's copy
// (web/espace/src/strings.json) is the same as the tables: after a change,
// ERIPHE_UPDATE_ESPACE=1 go test -run TestEspaceStrings writes it again.
func TestEspaceStrings(t *testing.T) {
	if fr, en := espaceKeys("fr"), espaceKeys("en"); !slices.Equal(fr, en) {
		t.Errorf("keys differ between fr and en:\nfr %v\nen %v", fr, en)
	}
	const file = "web/espace/src/strings.json"
	want := espaceStringsFile()
	if os.Getenv("ERIPHE_UPDATE_ESPACE") != "" {
		if err := os.WriteFile(file, want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(got, want) {
		t.Errorf("%s is not the words of web_text_espace.go (%v): run ERIPHE_UPDATE_ESPACE=1 go test -run TestEspaceStrings", file, err)
	}
}
