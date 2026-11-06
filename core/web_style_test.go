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
	files := []string{"app.css", "memory.css", "agents.css", "remote.css"}
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
