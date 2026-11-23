package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Nodes are the user's notes: never lost. No code of core removes the
// nodes' repository, a node's file or its trace, nor drops the
// repository's history. A node is only archived, by the user; a change of
// the file format comes with its conversion (nodefile.Upgrade).

// nodeRemovals are the removals and renames the store makes, each with
// why it loses no node: "<file>:<func>".
var nodeRemovals = map[string]string{
	"nodestore/backup.go:Backup":      "the backup's own copies: its temporary file, and the copies older than the ones it keeps",
	"nodestore/backup.go:writeTraces": "the traces' tar written aside, then renamed once whole",
	"nodestore/backup.go:writeBundle": "the bundle written aside, removed when it failed, renamed once checked",
	"nodestore/git.go:commit":         "commits the removal of a node's file already removed by hand (reconcile): the history keeps it",
	"nodestore/git.go:writeFile":      "writes a node's file whole, its new bytes renamed over the old ones, which the history keeps",
	"nodestore/trace.go:appendTrace":  "a trace past MaxTraceFile becomes its older part, the one before dropped (docs/node-format.md)",
}

// historyDropping are go-git's operations that drop history, objects or
// the worktree's content: the store calls none, nor any method so named
// (a buffer's Reset too: the guard does not know types).
var historyDropping = map[string]bool{
	"Prune": true, "RepackObjects": true, "DeleteObject": true, "DeleteLooseObject": true,
	"DeleteOldObjectPackAndIndex": true, "Reset": true, "ResetSparsely": true, "Clean": true,
	"Restore": true, "DeleteBranch": true, "DeleteTag": true, "RemoveReference": true, "RemoveRef": true,
}

// removals are the calls that remove or replace a file by its path
// (os.Truncate too).
var removals = map[string]bool{"Remove": true, "RemoveAll": true, "RemoveGlob": true, "Rename": true}

// nodeWords tell code that may reach the nodes: their folder, their
// traces', the data folder they are in (Config.DB's).
var nodeWords = regexp.MustCompile(`(?i:node|trace)|\.DB\b`)

func TestNodesAreNeverLost(t *testing.T) {
	used := map[string]bool{}
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path != "." && (d.Name() == "testdata" || d.Name() == "node_modules" || strings.HasPrefix(d.Name(), ".")) {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		hits, err := nodeLossHits(filepath.ToSlash(path), src, used)
		if err != nil {
			return err
		}
		for _, h := range hits {
			t.Errorf("%s (nodes are never lost: a node is archived, a format converted; see AGENTS.md)", h)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Each known removal is still there: the walk read the store, and
	// the list says no more than the code does.
	for at := range nodeRemovals {
		if !used[at] {
			t.Errorf("nodeRemovals: %s removes nothing any more: take it out", at)
		}
	}
}

// nodeLossHits are what may lose a node in a file of core (path from
// core's folder):
//   - go-git outside the store; in the store, one of its operations that
//     drops history, or any force option;
//   - git run as a program;
//   - in the store, a removal or a rename not in nodeRemovals;
//   - elsewhere, a removal or a rename in a function that names the nodes.
//
// used gets the store's removals it met, by "<file>:<func>".
func nodeLossHits(path string, src []byte, used map[string]bool) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		return nil, err
	}
	store := strings.HasPrefix(path, "nodestore/")
	text := func(n ast.Node) []byte { return src[fset.Position(n.Pos()).Offset:fset.Position(n.End()).Offset] }
	var hits []string
	hit := func(n ast.Node, what string) {
		hits = append(hits, fset.Position(n.Pos()).String()+": "+what)
	}
	imported := map[string]bool{} // the packages but go-git's, by the name code calls them
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if strings.HasPrefix(p, "github.com/go-git/") {
			if !store {
				hit(imp, "imports go-git outside nodestore")
			}
			continue
		}
		name := p[strings.LastIndex(p, "/")+1:]
		if imp.Name != nil {
			name = imp.Name.Name
		}
		imported[name] = true
	}
	for _, decl := range f.Decls {
		fn := ""
		if d, ok := decl.(*ast.FuncDecl); ok {
			fn = d.Name.Name
		}
		// Outside the store, a function that names the nodes removes
		// nothing: its paths may be theirs.
		nodesFunc := !store && nodeWords.Match(text(decl))
		ast.Inspect(decl, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.KeyValueExpr:
				if k, ok := x.Key.(*ast.Ident); ok && k.Name == "Force" {
					hit(x, "a Force option")
				}
			case *ast.CallExpr:
				sel, ok := x.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				name, pkg := sel.Sel.Name, ""
				if id, ok := sel.X.(*ast.Ident); ok {
					pkg = id.Name
				}
				removal := removals[name] && (pkg == "os" || !imported[pkg]) || pkg == "os" && name == "Truncate"
				switch {
				case pkg == "exec" && (name == "Command" || name == "CommandContext") && runsGit(x):
					hit(x, "runs git")
				case store && historyDropping[name] && !imported[pkg]:
					hit(x, name+": drops history")
				case store && removal:
					if _, ok := nodeRemovals[path+":"+fn]; !ok {
						hit(x, name+" in the store, not in nodeRemovals")
					}
					used[path+":"+fn] = true
				case pkg == "os" && removal && nodesFunc:
					hit(x, "os."+name+" where the nodes are named")
				}
			}
			return true
		})
	}
	return hits, nil
}

// runsGit tells an exec.Command whose program is git.
func runsGit(call *ast.CallExpr) bool {
	for _, a := range call.Args[:min(2, len(call.Args))] {
		if lit, ok := a.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			s, _ := strconv.Unquote(lit.Value)
			return s == "git"
		}
	}
	return false
}

// The guard catches a removal of the nodes, a history dropped, git run as
// a program; it lets the store's known removals and other folders be.
func TestNodesGuardCatches(t *testing.T) {
	for _, c := range []struct {
		path, src string
		hits      int
	}{
		{"x.go", `func wipe(c *Config) { dir, _ := c.nodesPaths(); os.RemoveAll(dir) }`, 1},
		{"x.go", `func wipe(data string) { os.RemoveAll(filepath.Join(data, "nodes")) }`, 1},
		{"x.go", `func wipe(c *Config) { os.RemoveAll(filepath.Dir(c.DB)) }`, 1},
		{"x.go", `func wipe(s *store) { os.Remove(s.tracePath("a1")) }`, 1},
		{"x.go", `func gc() { exec.Command("git", "gc", "--prune=now").Run() }`, 1},
		{"x.go", `import git "github.com/go-git/go-git/v5"`, 1},
		{"nodestore/x.go", `func reset(wt *git.Worktree) { wt.Reset(&git.ResetOptions{Mode: git.HardReset}) }`, 1},
		{"nodestore/x.go", `func push(r *git.Repository) { r.Push(&git.PushOptions{Force: true}) }`, 1},
		{"nodestore/x.go", `func prune(r *git.Repository) { r.Prune(git.PruneOptions{}); r.RepackObjects(nil) }`, 2},
		{"nodestore/x.go", `func drop(path string) { os.Remove(path) }`, 1},
		{"nodestore/x.go", `func drop(wt *git.Worktree) { wt.Remove("a-a1.md") }`, 1},
		{"nodestore/backup.go", `func Backup(tmp string) { os.Remove(tmp) }`, 0},
		{"nodestore/x.go", "import \"bytes\"\n\nfunc f(t time.Time) { t.Truncate(0); bytes.Clone(nil) }", 0},
		{"x.go", `func clean(r *remote) { os.RemoveAll(r.dir); buf.Reset() }`, 0},
	} {
		hits, err := nodeLossHits(c.path, []byte("package x\n\n"+c.src+"\n"), map[string]bool{})
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != c.hits {
			t.Errorf("%s %q: %d hits, want %d: %v", c.path, c.src, len(hits), c.hits, hits)
		}
	}
}
