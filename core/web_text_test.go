package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
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

// Every text a template names resolves in both languages: a literal
// {{t "key"}}, a key built over a list written in the template ({{range
// list "a" "b"}} … {{t (printf "prefix.%s" .)}}), and the keys built from
// the kinds the pages list (the journal's tabs, the memory's filters).
// French is not a fallback here: a key missing in one table shows raw.
func TestTemplateTextsResolve(t *testing.T) {
	has := func(key string) {
		t.Helper()
		for _, lang := range []string{"fr", "en"} {
			if _, ok := webStrings[lang][key]; !ok {
				t.Errorf("%s has no text %q", lang, key)
			}
		}
	}
	files, _ := filepath.Glob("web/templates/*.html")
	if len(files) == 0 {
		t.Fatal("no templates found")
	}
	literal := regexp.MustCompile(`\{\{-?\s*t "([^"]+)"`)
	ranged := regexp.MustCompile(`(?s)\{\{range list ((?:"[^"]*"\s*)+)\}\}(.*?)\{\{end\}\}`)
	built := regexp.MustCompile(`t \(printf "([^"%]*)%s([^"%]*)" \.\)`)
	word := regexp.MustCompile(`"([^"]*)"`)
	for _, name := range files {
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range literal.FindAllStringSubmatch(string(raw), -1) {
			has(m[1])
		}
		for _, r := range ranged.FindAllStringSubmatch(string(raw), -1) {
			for _, b := range built.FindAllStringSubmatch(r[2], -1) {
				for _, w := range word.FindAllStringSubmatch(r[1], -1) {
					has(b[1] + w[1] + b[2])
				}
			}
		}
	}
	for _, k := range happenedKinds {
		has("happened.kind." + k)
	}
}
