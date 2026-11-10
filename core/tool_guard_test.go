package main

import (
	"go/ast"
	"go/parser"
	"go/token"
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
			if strings.Contains(x.Value, "github.com/Alexis-Marcel/eriphe-system") {
				return true // Eriphe's own home
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
