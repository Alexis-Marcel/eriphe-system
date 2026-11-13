package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Every table of texts (map[string]map[string]string, one map per
// language) is merged into webStrings at start, the last written winning in
// silence: a key is defined once, in one table.
func TestTextKeysDefinedOnce(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	seen := map[string]string{} // lang/key: where
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok || !isTextTable(lit.Type) {
				return true
			}
			for _, e := range lit.Elts {
				kv, ok := e.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				lang := stringLit(kv.Key)
				words, ok := kv.Value.(*ast.CompositeLit)
				if lang == "" || !ok {
					continue
				}
				for _, w := range words.Elts {
					wkv, ok := w.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					key := stringLit(wkv.Key)
					if key == "" {
						continue
					}
					at := fset.Position(wkv.Pos()).String()
					if before, dup := seen[lang+"/"+key]; dup {
						t.Errorf("%s %q defined twice: %s and %s", lang, key, before, at)
					}
					seen[lang+"/"+key] = at
				}
			}
			return false
		})
	}
	if len(seen) < 100 {
		t.Fatalf("only %d texts found: the tables are not read", len(seen))
	}
}

// isTextTable is the type map[string]map[string]string.
func isTextTable(e ast.Expr) bool {
	m, ok := e.(*ast.MapType)
	if !ok || !isIdent(m.Key, "string") {
		return false
	}
	in, ok := m.Value.(*ast.MapType)
	return ok && isIdent(in.Key, "string") && isIdent(in.Value, "string")
}

func isIdent(e ast.Expr, name string) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == name
}

func stringLit(e ast.Expr) string {
	b, ok := e.(*ast.BasicLit)
	if !ok || b.Kind != token.STRING {
		return ""
	}
	s, err := strconv.Unquote(b.Value)
	if err != nil {
		return ""
	}
	return s
}
