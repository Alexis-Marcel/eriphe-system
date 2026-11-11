package main

import (
	"regexp"
	"strings"
	"testing"
)

// The workspace's look holds in its stylesheets: neutral surfaces, no hue
// gradient, no gradient text, no shouted labels, and every variable used is
// one a stylesheet defines (or app.js sets).
func TestStylesheetsKeepTheLook(t *testing.T) {
	files := []string{"app.css", "memory.css", "agents.css", "remote.css", "sessions.css"}
	var all strings.Builder
	for _, f := range files {
		css, err := webFS.ReadFile("web/static/" + f)
		if err != nil {
			t.Fatal(err)
		}
		all.Write(css)
		for _, banned := range []string{"background-clip: text", "text-transform: uppercase", "conic-gradient", "radial-gradient", "backdrop-filter"} {
			if strings.Contains(string(css), banned) {
				t.Errorf("%s uses %q", f, banned)
			}
		}
	}
	css := all.String()
	defined := map[string]bool{"--app-h": true}
	for _, m := range regexp.MustCompile(`(--[a-z0-9-]+):`).FindAllStringSubmatch(css, -1) {
		defined[m[1]] = true
	}
	for _, m := range regexp.MustCompile(`var\((--[a-z0-9-]+)`).FindAllStringSubmatch(css, -1) {
		if !defined[m[1]] {
			t.Errorf("%s is used but never defined", m[1])
		}
	}
}

// A tint is a colour the stylesheet knows, and a name keeps its own.
func TestTints(t *testing.T) {
	css, err := webFS.ReadFile("web/static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range tints {
		if !strings.Contains(string(css), ".tint-"+name+",") {
			t.Errorf("no .tint-%s in app.css", name)
		}
	}
	if tint("compta") != tint("compta") {
		t.Error("a name changes colour")
	}
	seen := map[string]bool{}
	for _, name := range []string{"compta", "photos", "maison", "notes", "agenda", "banque"} {
		seen[tint(name)] = true
	}
	if len(seen) < 2 {
		t.Errorf("every name wears the same colour: %v", seen)
	}
}

// On a phone the single grid column must be allowed to shrink below its
// content, or a long line widens the whole page and pushes the send button
// off screen.
func TestPhoneColumnCanShrink(t *testing.T) {
	css, err := webFS.ReadFile("web/static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(css), ".workspace, .workspace.side-off { grid-template-columns: minmax(0, 1fr); }") {
		t.Error("the phone workspace column is not minmax(0, 1fr)")
	}
}

// A stretched link's layer stays inside its tile: every tile that holds one
// is positioned, or a click anywhere on the page opens it.
func TestStretchedLinksStayInTheirTile(t *testing.T) {
	css, err := webFS.ReadFile("web/static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(css), ".tile:has(.stretched) { position: relative;") {
		t.Fatal("a tile with a stretched link is not positioned")
	}
}
