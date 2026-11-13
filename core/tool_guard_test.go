package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// toolData are the files where a tool or GitHub may be named, besides
// providerData: the catalog's cards and presets, and GitHub's own code, the
// provider of the git access and of its app's connections. Everywhere
// else in core, a tool is what its card says, and the git access is "git".
var toolData = []string{
	"github_app.go", "web_github.go", // the app: its connections, its page
	"git_access.go", "repo_lend.go", "repo_skills.go", // git and gh through GitHub
	"memory_trust.go", // the shapes of secrets, GitHub's tokens among them
}

// toolNamed is what the guard looks for: GitHub, and the cards and kinds
// of Eriphe's own tools taken by name.
var toolNamed = regexp.MustCompile(`(?i)github|^(toolMail|toolCalendar|toolFiles|mailService|calendarService|filesService|mailShape|calendarShape)$`)

// No logic file of core names a tool or GitHub: imports, and Eriphe's own
// home on GitHub, aside.
func TestNoToolInLogic(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || strings.HasPrefix(f, "web_text") || slices.Contains(providerData, f) || slices.Contains(toolData, f) {
			continue
		}
		for _, hit := range toolHits(t, f) {
			t.Errorf("%s names a tool: %s (a tool is its card's data: tool_cards.go, presets.go; GitHub's code is in toolData)", f, hit)
		}
	}
}

// toolHits are the identifiers and strings of a file that name a tool,
// with their line.
func toolHits(t *testing.T, file string) []string {
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
		case *ast.ImportSpec:
			return false
		case *ast.Ident:
			s = x.Name
		case *ast.BasicLit:
			if strings.Contains(x.Value, "github.com/Alexis-Marcel/eriphe-system") || strings.Contains(x.Value, "alexis-marcel.github.io/eriphe-system") {
				return true // Eriphe's own home, and its pages (its OAuth client, cimd.go)
			}
			s = x.Value
		default:
			return true
		}
		if toolNamed.MatchString(s) {
			hits = append(hits, fset.Position(n.Pos()).String()+" "+s)
		}
		return true
	})
	return hits
}

// The guard catches a tool named in code or a string, and lets an import,
// a comment and Eriphe's home be.
func TestToolGuardCatches(t *testing.T) {
	file := filepath.Join(t.TempDir(), "x.go")
	os.WriteFile(file, []byte(`package main

import _ "github.com/example/lib"

// GitHub, for instance.
const home = "https://github.com/Alexis-Marcel/eriphe-system"

func f(p string) bool { return p == "github" || len(mailService.ID) > 0 }
`), 0o600)
	hits := toolHits(t, file)
	if len(hits) != 2 || !strings.Contains(hits[0], `"github"`) || !strings.Contains(hits[1], "mailService") {
		t.Fatalf("hits %v", hits)
	}
}

// workspaceFiles are the workspace's surfaces and what they stand on: no
// tool or server is known there, so a tool linked tomorrow is a source
// with no code (#415). Patterns, so that a file added there is guarded.
var workspaceFiles = []string{"surface*.go", "workspace*.go", "web_espace*.go", "web_surfaces.go", "web_text_surfaces.go", "web_text_espace.go", "thread_surface.go", "pool_follow.go", "default_skills.go", "composition.go", "a2ui/*.go"}

// toolWords are the names a tool goes by in eriphe-core's own data, taken
// from the data so a preset added is guarded too: words, the names of
// Eriphe's own presets, cards and sign-in providers, which no identifier
// or string may hold; names, those of the gallery's presets (everyday
// words, buffer or make) and their hosts, which no string may be.
func toolWords() (words, names map[string]bool) {
	words, names = map[string]bool{}, map[string]bool{}
	norm := regexp.MustCompile(`[^a-z0-9]+`)
	add := func(s string) {
		for _, w := range identWords(s) {
			words[w] = true
		}
		if s != "" {
			names[norm.ReplaceAllString(strings.ToLower(s), "")] = true
		}
	}
	for _, p := range toolPresets {
		if p.Category != "" {
			names[norm.ReplaceAllString(strings.ToLower(p.ID), "")] = true
			if u, err := url.Parse(p.URL); err == nil && u.Hostname() != "" {
				names[u.Hostname()] = true
			}
			continue
		}
		if p.ID == "remote" || p.ID == "local" {
			continue // the forms of a tool of one's own, not a tool
		}
		add(p.ID)
		add(p.Name)
	}
	for _, c := range toolCards {
		add(c.ID)
		add(c.Base)
		add(c.Preset)
	}
	for name := range signInProviders {
		add(name)
	}
	return words, names
}

// identWords splits an identifier or a string into lowercase words, at
// case changes and anything not a letter or a digit.
func identWords(s string) []string {
	s = regexp.MustCompile(`([a-z0-9])([A-Z])`).ReplaceAllString(s, "$1 $2")
	return regexp.MustCompile(`[a-z0-9]+`).FindAllString(strings.ToLower(s), -1)
}

// The workspace's surfaces name no tool, server or provider, in code or
// strings (comments aside): what a source reads is the author's data.
func TestNoToolInSurfaces(t *testing.T) {
	words, names := toolWords()
	if !words["mail"] || !names["github"] || !names["buffer"] || len(words) < 5 {
		t.Fatalf("words %v, names %v", words, names)
	}
	for _, pattern := range workspaceFiles {
		files, _ := filepath.Glob(pattern)
		if len(files) == 0 {
			t.Fatalf("no file %s", pattern)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, f, src, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(file, func(n ast.Node) bool {
				var s string
				switch x := n.(type) {
				case *ast.ImportSpec:
					return false
				case *ast.Ident:
					s = x.Name
				case *ast.BasicLit:
					s = x.Value
					lit := strings.ToLower(strings.Trim(s, "`\""))
					if names[regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(lit, "")] {
						t.Errorf("%s names a tool: %s", fset.Position(n.Pos()), s)
					}
					for host := range names {
						if strings.Contains(host, ".") && strings.Contains(lit, host) {
							t.Errorf("%s names a tool's host: %s", fset.Position(n.Pos()), s)
						}
					}
				default:
					return true
				}
				for _, w := range identWords(s) {
					if words[w] {
						t.Errorf("%s names a tool: %q in %s", fset.Position(n.Pos()), w, s)
					}
				}
				return true
			})
		}
	}
}
