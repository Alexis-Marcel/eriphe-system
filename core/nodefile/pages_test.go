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

// An item's row is the outermost item holding it: what a reader sees
// without opening a detail, in a callout too; none in a callout that
// draws its lists its own way (figures), or folded.
func TestPageRow(t *testing.T) {
	p := ReadPage("## À traiter\n- [ ] Le devis [Sam][m1]\n  - Voir [la pièce][m2]\n    - Plus bas [encore][m3]\n- Seul [m4][]\n\n> [!note] Côté\n> - Dans [un encadré][m5]\n>   - Sous [lui][m6]\n\nUn [paragraphe][m7].\n\n> [!figures] Chiffres\n> - Compte [m8][] : 12 €\n\n> [!warning]- Replié\n> - Caché [m9][]\n")
	rows := map[string]int{}
	for _, u := range p.Uses {
		rows[u.Label] = p.Row(u.Item)
	}
	want := map[string]int{"m1": 2, "m2": 2, "m3": 2, "m4": 5, "m5": 8, "m6": 8, "m7": 0, "m8": 0, "m9": 0}
	for k, v := range want {
		if rows[k] != v {
			t.Errorf("%s: row %d, want %d (uses %+v)", k, rows[k], v, p.Uses)
		}
	}
	if p.Unseen(16) || !p.Unseen(17) || p.Unseen(2) {
		t.Errorf("unseen: a folded callout's title %v, its body %v, a row %v", p.Unseen(16), p.Unseen(17), p.Unseen(2))
	}
	if p.Row(3) != 2 || p.Row(1) != 0 || p.Row(0) != 0 {
		t.Errorf("rows of lines: %d %d %d", p.Row(3), p.Row(1), p.Row(0))
	}
}
