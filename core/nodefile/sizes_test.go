package nodefile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
)

// fences are a node body's blocks as its author writes them, fences
// included: what a model writes for a page besides its text.
var fences = regexp.MustCompile("(?s)```eriphe:block [^\n]*\n.*?\n```\n")

// The same six pages, written with the blocks a model wrote before
// (List, Item, Fold, Grid, Card, Metric, Button, Progress: still read)
// and with the ones it writes now (Sections, Figures, Gallery, Asks, a
// Table that infers its columns, a Timeline that says its progress): both
// valid, the same text, and the blocks about half as long.
func TestPageSizes(t *testing.T) {
	befores, _ := filepath.Glob("testdata/sizes/*.before.md")
	if len(befores) != 6 {
		t.Fatalf("%d pages", len(befores))
	}
	var before, after int
	for _, b := range befores {
		page := strings.TrimSuffix(filepath.Base(b), ".before.md")
		size := map[string]int{}
		for _, side := range []string{"before", "after"} {
			raw, err := os.ReadFile(filepath.Join("testdata/sizes", page+"."+side+".md"))
			if err != nil {
				t.Fatal(err)
			}
			n, err := authorNew(string(raw))
			if err != nil {
				t.Errorf("%s, %s: %v", page, side, err)
				continue
			}
			for _, b := range n.Blocks() {
				var comps []map[string]any
				json.Unmarshal(b.Components, &comps)
				for _, c := range comps {
					if kind, _ := c["component"].(string); side == "after" && a2ui.IsLegacy(kind) {
						t.Errorf("%s, block %s: %s is no longer written", page, b.Name, kind)
					}
				}
			}
			for _, f := range fences.FindAllString(string(raw), -1) {
				size[side] += len([]rune(f))
			}
		}
		t.Logf("%-12s %5d → %5d (%+d %%)", page, size["before"], size["after"], (size["after"]-size["before"])*100/size["before"])
		if size["after"] >= size["before"] {
			t.Errorf("%s: %d → %d", page, size["before"], size["after"])
		}
		before += size["before"]
		after += size["after"]
	}
	t.Logf("%-12s %5d → %5d (%+d %%)", "all", before, after, (after-before)*100/before)
	if after*100 > before*60 {
		t.Errorf("the blocks went from %d to %d characters: not about half", before, after)
	}
}
