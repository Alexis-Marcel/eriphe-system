package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The engine runs Eriphe's images and those the machine adds, no other.
func TestImagePolicy(t *testing.T) {
	file := filepath.Join(t.TempDir(), "images")
	p := &imagePolicy{allowed: splitList(defaultImages + ", ghcr.io/example/node:2"), file: file}
	allowed := func(image string) bool { return p.check(image) == nil }
	for image, want := range map[string]bool{
		"ghcr.io/alexis-marcel/eriphe-base:latest":     true,
		"ghcr.io/alexis-marcel/eriphe-mail@sha256:abc": true,
		"GHCR.IO/Alexis-Marcel/eriphe-files":           true,
		"ghcr.io/alexis-marcel-evil/x":                 false,
		"ghcr.io/alexis-marcel":                        false,
		"ghcr.io/example/node:2":                       true,
		"ghcr.io/example/node:3":                       false,
		"mcp/time":                                     false,
		"alpine":                                       false,
	} {
		if got := allowed(image); got != want {
			t.Errorf("%s: allowed %v, want %v", image, got, want)
		}
	}

	// The machine adds some, read again at each request.
	os.WriteFile(file, []byte("# tools\nmcp/\ndocker.io/alpine\nghcr.io/example/tool@sha256:abc\n"), 0o600)
	for image, want := range map[string]bool{
		"mcp/time":                         true,
		"docker.io/mcp/time:1":             true,
		"index.docker.io/mcp/fetch":        true,
		"mcpx/time":                        false,
		"alpine:3":                         true,
		"library/alpine":                   true,
		"ghcr.io/example/tool@sha256:abc":  true,
		"ghcr.io/example/tool@sha256:abd":  false,
		"ghcr.io/example/tool:latest":      false,
		"localhost:5000/alexis-marcel/x:1": false,
	} {
		if got := allowed(image); got != want {
			t.Errorf("%s: allowed %v, want %v", image, got, want)
		}
	}
	if !imageAllowed("*", "anything/at:all") {
		t.Error("* allows nothing")
	}
}

// A task in an image the machine does not allow is refused.
func TestEngineRefusesAnImage(t *testing.T) {
	s := &engineServer{images: &imagePolicy{allowed: []string{defaultImages}}}
	if err := s.checkImage("ghcr.io/alexis-marcel/eriphe-base"); err != nil {
		t.Error(err)
	}
	if err := s.checkImage("ghcr.io/example/worker"); err == nil {
		t.Error("an image from elsewhere is allowed")
	}
}
