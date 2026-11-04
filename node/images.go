package main

import (
	"fmt"
	"os"
	"strings"
)

// The images eriphe-node engine runs for tasks and services. eriphe-core
// names them (an agent's image, a tool's), and the node passes them on: a
// node or a core taken over could otherwise have the engine pull and run any
// image, and an image is more than the code it runs (its layers, its
// configuration), what a container escape starts from. So the machine says
// which it allows: Eriphe's own by default, more in -images or in a file of
// its own (-image-file), read again at each request.

// defaultImages are Eriphe's own images.
const defaultImages = "ghcr.io/alexis-marcel/"

// imagePolicy is the images the machine allows: a repository (any of its
// tags), a repository at a tag or a digest (that one only), a namespace
// (REGISTRY/NAMESPACE/, all of its repositories) or * (any).
type imagePolicy struct {
	allowed []string
	file    string
}

func (p *imagePolicy) check(image string) error {
	for _, a := range append(p.allowed, p.listed()...) {
		if imageAllowed(a, image) {
			return nil
		}
	}
	return fmt.Errorf("image %s is not allowed on this machine (eriphe-node engine -images, -image-file)", image)
}

// listed is the images of -image-file now: none when it cannot be read.
func (p *imagePolicy) listed() []string {
	if p.file == "" {
		return nil
	}
	raw, err := os.ReadFile(p.file)
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(string(raw), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	return out
}

// imageAllowed: image is one allow names.
func imageAllowed(allow, image string) bool {
	if allow == "*" {
		return true
	}
	repo, ref := normalImage(image)
	if ns, ok := strings.CutSuffix(allow, "/"); ok {
		// Normalized as a repository in it would be: mcp/ is docker.io/mcp/.
		in, _ := normalImage(ns + "/x")
		return strings.HasPrefix(repo, strings.TrimSuffix(in, "x"))
	}
	allowRepo, allowRef := normalImage(allow)
	return repo == allowRepo && (allowRef == "" || allowRef == ref)
}

// normalImage is an image's repository, in full (docker.io/library/alpine
// for alpine), and its tag (:1) or digest (@sha256:…), if it has one.
func normalImage(image string) (repo, ref string) {
	repo = image
	if i := strings.IndexByte(repo, '@'); i >= 0 {
		repo, ref = repo[:i], repo[i:]
	} else if i := strings.LastIndexByte(repo, ':'); i > strings.LastIndexByte(repo, '/') {
		repo, ref = repo[:i], repo[i:]
	}
	repo = strings.ToLower(repo)
	first, rest, found := strings.Cut(repo, "/")
	switch {
	case !found:
		repo = "docker.io/library/" + repo
	case first == "docker.io" || first == "index.docker.io":
		if !strings.Contains(rest, "/") {
			rest = "library/" + rest
		}
		repo = "docker.io/" + rest
	case !strings.ContainsAny(first, ".:") && first != "localhost":
		repo = "docker.io/" + repo
	}
	return repo, ref
}

// splitList is a comma-separated list's items.
func splitList(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
