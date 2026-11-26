package nodefile

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The page fixtures (testdata/pages): each <name>.md and <name>.json,
// the tree ReadPage gives (page), what Check finds (problems) with the
// tools its author has (tools; none: not checked). The workspace's reader
// is held to the same files. go test -run TestPageFixtures -update writes
// them again from the reader: read the diff before keeping it.

var update = flag.Bool("update", false, "write the page fixtures' JSON again")

type pageFixture struct {
	Tools    []string  `json:"tools,omitempty"`
	Page     *Page     `json:"page"`
	Problems []Problem `json:"problems"`
}

func TestPageFixtures(t *testing.T) {
	mds, _ := filepath.Glob("testdata/pages/*.md")
	if len(mds) < 10 {
		t.Fatalf("%d fixture pages", len(mds))
	}
	for _, md := range mds {
		name := strings.TrimSuffix(filepath.Base(md), ".md")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(md)
			if err != nil {
				t.Fatal(err)
			}
			path := strings.TrimSuffix(md, ".md") + ".json"
			var want pageFixture
			if raw, err := os.ReadFile(path); err == nil {
				if err := json.Unmarshal(raw, &want); err != nil {
					t.Fatal(err)
				}
			} else if !*update {
				t.Fatal(err)
			}
			var has func(string) bool
			if want.Tools != nil {
				has = func(tool string) bool { return slices.Contains(want.Tools, tool) }
			}
			p := ReadPage(string(src))
			got := pageFixture{Tools: want.Tools, Page: p, Problems: p.Check(has)}
			var buf bytes.Buffer
			enc := json.NewEncoder(&buf)
			enc.SetEscapeHTML(false)
			enc.SetIndent("", "  ")
			if err := enc.Encode(got); err != nil {
				t.Fatal(err)
			}
			out := buf.Bytes()
			if *update {
				if err := os.WriteFile(path, out, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			raw, _ := os.ReadFile(path)
			if !bytes.Equal(normJSON(t, raw), normJSON(t, out)) {
				t.Errorf("%s reads otherwise than %s:\n%s", md, path, out)
			}
			faulty := strings.HasPrefix(name, "faulty-")
			if faulty != (len(got.Problems) > 0) {
				t.Errorf("faulty page %v, problems %v", faulty, got.Problems)
			}
		})
	}
}

func normJSON(t *testing.T, raw []byte) []byte {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(v)
	return out
}
