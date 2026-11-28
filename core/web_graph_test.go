package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The workspace's fonts come from eriphe-core itself, beside its stylesheet,
// as fonts: the CSP allows no other source.
func TestEspaceFonts(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	_, css, _ := tw.do("GET", "/static/espace/espace.css", nil, nil)
	urls := regexp.MustCompile(`url\(\./(espace-[A-Za-z-]+\.woff2)\)`).FindAllStringSubmatch(css, -1)
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

// The fonts go with their license, beside them in the sources: Geist is
// under the SIL Open Font License, which travels with every copy.
func TestEspaceFontsLicense(t *testing.T) {
	ofl, err := os.ReadFile("web/espace/src/fonts/OFL.txt")
	if err != nil || !bytes.Contains(ofl, []byte("SIL OPEN FONT LICENSE Version 1.1")) {
		t.Fatalf("OFL.txt: %v", err)
	}
	fonts, _ := filepath.Glob("web/espace/src/fonts/*.woff2")
	if len(fonts) != 2 {
		t.Errorf("fonts: %v", fonts)
	}
}

// The built app carries none of the dev server's made-up workspace (its
// samples and gallery, web/espace/src/dev.tsx), and its one chunk (the
// .md tab's editor) imports nothing of the entry's: loaded without the
// entry's ?v=, it would load a second app.
func TestEspaceBundle(t *testing.T) {
	entry, err := fs.ReadFile(webFS, "web/static/espace/espace.js")
	if err != nil {
		t.Fatal(err)
	}
	// One line each of sample.ts, sampleviews.ts, fixtures.ts, gallery.json,
	// dev-leger.tsx (the ?leger page).
	for _, s := range []string{"Virement urgent à valider", "Temps de réponse p95", "Relire le devis de Sam", "Le train de 7 h 12", "nœuds fictifs"} {
		if bytes.Contains(entry, []byte(s)) {
			t.Errorf("espace.js holds a sample's %q", s)
		}
	}
	chunks, _ := fs.Glob(webFS, "web/static/espace/espace-*.js")
	if len(chunks) != 2 {
		t.Fatalf("chunks: %v", chunks)
	}
	for _, c := range chunks {
		if !bytes.Contains(entry, []byte(strings.TrimPrefix(c, "web/static/espace/"))) {
			t.Errorf("%s is not loaded by the entry", c)
		}
		raw, _ := fs.ReadFile(webFS, c)
		if regexp.MustCompile(`(from|import\s*\(?)\s*["'\x60]\./`).Match(raw) {
			t.Errorf("%s imports a module", c)
		}
	}
}

// pdf.js's worker (web/espace/src/doc/pdf.ts) is served from eriphe-core
// itself, beside its chunk, as JavaScript (a module worker is refused as
// anything else), and the workspace's CSP lets the page start it.
func TestEspacePdfWorker(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	workers, _ := fs.Glob(webFS, "web/static/espace/espace-pdf.worker.min-*.mjs")
	pdf, _ := fs.Glob(webFS, "web/static/espace/espace-pdf-*.js")
	if len(workers) != 1 || len(pdf) != 1 {
		t.Fatalf("worker %v, chunk %v", workers, pdf)
	}
	name := strings.TrimPrefix(workers[0], "web/static/espace/")
	if raw, _ := fs.ReadFile(webFS, pdf[0]); !bytes.Contains(raw, []byte(name)) {
		t.Errorf("%s does not start %s", pdf[0], name)
	}
	code, _, h := tw.do("GET", "/static/espace/"+name, nil, nil)
	if code != http.StatusOK || !strings.HasPrefix(h.Get("Content-Type"), "text/javascript") {
		t.Errorf("%s: %d %s", name, code, h.Get("Content-Type"))
	}
	_, _, h = tw.do("GET", "/espace", nil, nil)
	if csp := h.Get("Content-Security-Policy"); !strings.Contains(csp, "worker-src 'self'") || strings.Contains(csp, "unsafe-eval") {
		t.Errorf("csp: %s", csp)
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
