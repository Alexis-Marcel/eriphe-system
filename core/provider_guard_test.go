package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// providerData are the files where a provider's name may be written: its
// data (sign-in providers, the tools' cards, presets and their gallery, the
// model providers) and the words of the workspace. Everywhere else, a
// provider is data read from them, never a case in the code.
var providerData = []string{
	"signin_providers.go", "tool_cards.go", "presets.go", "presets_online.go", "web_gallery.go",
	"agents_kinds.go", "providers_check.go", "policy.go",
}

// providerName is what the guard looks for: Google's products by name. A
// files tool is "files", with places: a drive is one provider's word.
var providerName = regexp.MustCompile(`(?i)google|gmail|drive`)

// No logic file of core names a provider: in its code (identifiers and
// strings; comments may give one as an example), a provider is data.
func TestNoProviderInLogic(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || strings.HasPrefix(f, "web_text") || slices.Contains(providerData, f) {
			continue
		}
		for _, hit := range providerHits(t, f) {
			t.Errorf("%s names a provider: %s (make it data: signin_providers.go, presets.go, the text tables)", f, hit)
		}
	}
}

// providerHits are the identifiers and strings of a file that name a
// provider, with their line.
func providerHits(t *testing.T, file string) []string {
	t.Helper()
	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, src, 0) // without comments
	if err != nil {
		t.Fatal(err)
	}
	var hits []string
	ast.Inspect(f, func(n ast.Node) bool {
		var s string
		switch x := n.(type) {
		case *ast.Ident:
			s = x.Name
		case *ast.BasicLit:
			s = x.Value
		default:
			return true
		}
		if providerName.MatchString(s) {
			hits = append(hits, fset.Position(n.Pos()).String()+" "+s)
		}
		return true
	})
	return hits
}

// The guard catches a new mention, in a name or a string, and lets a
// comment be.
func TestProviderGuardCatches(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "x.go")
	os.WriteFile(file, []byte("package main\n\n// Gmail, for instance.\nfunc gmailCase(p string) bool { return p == \"google\" }\n"), 0o600)
	if hits := providerHits(t, file); len(hits) != 2 {
		t.Fatalf("hits: %v", hits)
	}
}

// runtimeData are the files where a runtime or a kind of model provider
// is named: their cards (harnesses.go, agents_kinds.go) and the URLs a
// provider's key is tested at.
var runtimeData = []string{"harnesses.go", "agents_kinds.go", "providers_check.go"}

// No logic file of core names a runtime or a kind of model provider in a
// string: what one can do is on its card, read by the code.
func TestNoRuntimeInLogic(t *testing.T) {
	names := map[string]bool{}
	for r := range harnesses {
		names[strconv.Quote(r)] = true
	}
	for _, k := range providerKinds {
		names[strconv.Quote(k.ID)] = true
	}
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || strings.HasPrefix(f, "web_text") || slices.Contains(runtimeData, f) {
			continue
		}
		for _, hit := range literalHits(t, f, func(s string) bool { return names[s] }) {
			t.Errorf("%s names a runtime or a provider kind: %s (put what it means on its card: harnesses.go, agents_kinds.go)", f, hit)
		}
	}
}

// literalHits are the strings of a file that match, with their line.
func literalHits(t *testing.T, file string, match func(string) bool) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var hits []string
	ast.Inspect(f, func(n ast.Node) bool {
		if x, ok := n.(*ast.BasicLit); ok && x.Kind == token.STRING && match(x.Value) {
			hits = append(hits, fset.Position(n.Pos()).String()+" "+x.Value)
		}
		return true
	})
	return hits
}

// The runtime guard catches a runtime's name in a string, not in a comment.
func TestRuntimeGuardCatches(t *testing.T) {
	file := filepath.Join(t.TempDir(), "x.go")
	os.WriteFile(file, []byte("package main\n\n// Like codex.\nfunc isCodex(r string) bool { return r == \"codex\" }\n"), 0o600)
	if hits := literalHits(t, file, func(s string) bool { return s == `"codex"` }); len(hits) != 1 {
		t.Fatalf("hits: %v", hits)
	}
}

// toolImage is what the tools guard looks for: an image of Eriphe's tools.
var toolImage = regexp.MustCompile(`eriphe-(mail|calendar|files)\b`)

// No logic file of core names one of its tools' images in a string: a
// tool is its card (tool_cards.go), found by what it declares.
func TestNoToolImageInLogic(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "tool_cards.go" {
			continue
		}
		for _, hit := range literalHits(t, f, toolImage.MatchString) {
			t.Errorf("%s names a tool's image: %s (it is data: tool_cards.go)", f, hit)
		}
	}
}

// The guard catches an image in a string, and lets a comment be.
func TestToolImageGuardCatches(t *testing.T) {
	file := filepath.Join(t.TempDir(), "x.go")
	os.WriteFile(file, []byte("package main\n\n// eriphe-mail, for instance.\nvar img = \"ghcr.io/x/eriphe-mail\"\n"), 0o600)
	if hits := literalHits(t, file, toolImage.MatchString); len(hits) != 1 {
		t.Fatalf("hits: %v", hits)
	}
}
