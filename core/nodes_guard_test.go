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
// the file format comes with its conversion (nodefile.Upgrade). The one
// loss known: a trace past MaxTraceFile drops its oldest part.

// nodeRemovals are the removals and renames the store makes, each call as
// written, with why it loses no node: "<file> <func> <call>".
var nodeRemovals = map[string]string{
	"nodestore/backup.go Backup os.Remove(tmp)":                          "the backup's temporary copy of the index",
	"nodestore/backup.go Backup os.Remove(db)":                           "the night's copy of the index, replaced by the one just made",
	"nodestore/backup.go Backup os.Rename(tmp, db)":                      "the index's copy takes its name once whole",
	"nodestore/backup.go Backup os.Remove(files[0])":                     "backup copies older than the ones it keeps",
	"nodestore/backup.go writeTraces os.Remove(tmp)":                     "the traces' tar written aside, when it failed",
	"nodestore/backup.go writeTraces os.Rename(tmp, path)":               "the traces' tar takes its name once whole",
	"nodestore/backup.go writeBundle os.Remove(tmp)":                     "the bundle written aside, when it failed",
	"nodestore/backup.go writeBundle os.Rename(tmp, path)":               "the bundle takes its name once checked",
	"nodestore/index.go openIndex os.Remove(f)":                          "the index (nodes.db, its -wal and -shm) at an older schema, made again from the files and the log",
	"nodestore/git.go commit wt.Remove(c.path)":                          "commits the removal of a file already removed by hand (reconcile's change without data): the history keeps it",
	"nodestore/git.go writeFile os.Rename(tmp, path)":                    "writes a node's file whole, its new bytes over the old ones, which the history keeps",
	"nodestore/trace.go appendTrace os.Rename(path, s.tracePath(id, 1))": "a trace past MaxTraceFile becomes its older part, the one before dropped (docs/node-format.md)",
	"nodestore/pack.go writePack os.Remove(tmpPack)":                     "the pack written aside, when it failed or once renamed",
	"nodestore/pack.go writePack os.Remove(tmpIdx)":                      "the pack's index written aside, when it failed or once renamed",
	"nodestore/pack.go writePack os.Rename(tmpPack, base+\".pack\")":     "the pack takes its name once read back whole",
	"nodestore/pack.go writePack os.Rename(tmpIdx, base+\".idx\")":       "its index takes its name after it",
}

// historyPacked are the calls of historyDropping the store makes, as
// nodeRemovals says them, with why they drop no history: Pack (git gc
// without prune) removes them once its pack, read back whole, holds every
// object the branch reaches and the repository opened again reads them.
var historyPacked = map[string]string{
	"nodestore/pack.go pack ls.DeleteLooseObject(h)":                        "a loose copy of an object the new pack holds",
	"nodestore/pack.go pack ps.DeleteOldObjectPackAndIndex(h, time.Time{})": "a pack before the new one, which holds what it holds that the branch reaches",
}

// historyDropping are go-git's operations that drop history, objects or
// the worktree's content, or move what the branch reaches: the store
// calls none, nor any method so named (a buffer's Reset too: the guard
// does not know types).
var historyDropping = map[string]bool{
	"Prune": true, "RepackObjects": true, "DeleteObject": true, "DeleteLooseObject": true,
	"DeleteOldObjectPackAndIndex": true, "Reset": true, "ResetSparsely": true, "Clean": true,
	"Restore": true, "DeleteBranch": true, "DeleteTag": true, "RemoveReference": true, "RemoveRef": true,
	"SetReference": true, "CheckAndSetReference": true,
}

// rewriting are the options of go-git that write over what was: the
// store sets none.
var rewriting = map[string]bool{"Force": true, "Amend": true}

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
	for at := range historyPacked {
		if !used[at] {
			t.Errorf("historyPacked: %s is not called any more: take it out", at)
		}
	}
}

// nodeLossHits are what may lose a node in a file of core (path from
// core's folder):
//   - go-git outside the store; in the store, one of its operations that
//     drops history, a Force or Amend option;
//   - git run as a program;
//   - in the store, a removal or a rename not in nodeRemovals, or a
//     change without data (a file removed) outside reconcile;
//   - elsewhere, a removal or a rename in a function that names the nodes.
//
// used gets the store's removals it met, as nodeRemovals says them.
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
				if k, ok := x.Key.(*ast.Ident); ok && store && rewriting[k.Name] {
					hit(x, "a "+k.Name+" option")
				}
			case *ast.CompositeLit:
				// A change without data removes a file: only reconcile
				// makes one, for a file already removed by hand.
				if !store || path == "nodestore/reconcile.go" {
					return true
				}
				if id, ok := x.Type.(*ast.Ident); ok && id.Name == "change" && !hasKey(x, "data") {
					hit(x, "a change without data removes a node's file")
				}
				// []change{{…}}: its elements' type is elided.
				if arr, ok := x.Type.(*ast.ArrayType); ok {
					if id, ok := arr.Elt.(*ast.Ident); ok && id.Name == "change" {
						for _, e := range x.Elts {
							if el, ok := e.(*ast.CompositeLit); ok && el.Type == nil && !hasKey(el, "data") {
								hit(el, "a change without data removes a node's file")
							}
						}
					}
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
					at := path + " " + fn + " " + string(text(x))
					if _, ok := historyPacked[at]; !ok {
						hit(x, name+": drops history")
					}
					used[at] = true
				case store && removal:
					at := path + " " + fn + " " + string(text(x))
					if _, ok := nodeRemovals[at]; !ok {
						hit(x, string(text(x))+" in the store, not in nodeRemovals")
					}
					used[at] = true
				case pkg == "os" && removal && nodesFunc:
					hit(x, "os."+name+" where the nodes are named")
				}
			}
			return true
		})
	}
	return hits, nil
}

// hasKey tells a composite literal that sets the field key.
func hasKey(lit *ast.CompositeLit, key string) bool {
	for _, e := range lit.Elts {
		if kv, ok := e.(*ast.KeyValueExpr); ok {
			if id, ok := kv.Key.(*ast.Ident); ok && id.Name == key {
				return true
			}
		}
	}
	return false
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
		{"nodestore/pack.go", `func pack(ls storer.LooseObjectStorer, h plumbing.Hash) error { return ls.DeleteLooseObject(h) }`, 0},
		{"nodestore/pack.go", `func gc(ls storer.LooseObjectStorer, h plumbing.Hash) error { return ls.DeleteLooseObject(h) }`, 1},
		{"nodestore/pack.go", `func pack(r *git.Repository) error { return r.RepackObjects(&git.RepackConfig{}) }`, 1},
		{"nodestore/x.go", `func drop(path string) { os.Remove(path) }`, 1},
		{"nodestore/x.go", `func drop(wt *git.Worktree) { wt.Remove("a-a1.md") }`, 1},
		{"nodestore/backup.go", `func Backup(tmp string) { os.Remove(tmp) }`, 0},
		{"nodestore/backup.go", `func Backup(s *Store) { os.RemoveAll(s.dir) }`, 1},
		{"nodestore/x.go", `func Delete(s *Store, file string) { s.commit("delete", "user", now, []change{{path: file}}, nil) }`, 1},
		{"nodestore/x.go", `func del(file string) []change { return []change{change{path: file}} }`, 1},
		{"nodestore/x.go", `func write(file string, raw []byte) []change { return []change{{path: file, data: raw}} }`, 0},
		{"nodestore/x.go", `func move(r *git.Repository, ref *plumbing.Reference) { r.Storer.SetReference(ref) }`, 1},
		{"nodestore/x.go", `func amend(wt *git.Worktree) { wt.Commit("x", &git.CommitOptions{Amend: true}) }`, 1},
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
