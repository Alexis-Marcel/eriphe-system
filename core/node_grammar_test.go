package main

import (
	"strings"
	"testing"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
)

// The grammar's example is a block nodefile and code accept as it is
// written: its sources, items and open, its sections' filters and its
// batch; and catalog_read gives the grammar after the components, to the
// brain and to a worker alike.
func TestNodeGrammarExample(t *testing.T) {
	n, err := nodefile.New(nodefile.Meta{ID: "abc123", Title: "Mes mails", Created: time.Now()}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := n.AuthorBody("Le bilan.\n\n```eriphe:block mails\n" + nodeGrammarExample + "\n```\n"); err != nil {
		t.Fatalf("the example is refused: %v", err)
	}
	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
	b := n.Block("mails")
	if b == nil || len(b.Sources) != 1 || b.Sources["mails"].Items == nil || b.Sources["mails"].Items.Open == nil {
		t.Fatalf("block %+v", b)
	}
	if !strings.Contains(string(b.Bound()), `"items":{"@path":"/mails/messages"}`) {
		t.Fatalf("its Sections does not read the source's items: %s", b.Bound())
	}
	c := graphCore(t)
	out := mustBrainNodes(t, c, "catalog_read", map[string]any{})
	catalog, _ := out["catalog"].(string)
	for _, want := range []string{"## Composants", "# Un bloc d'Eriphe", "`glance`", "`\"open\"", "**Sections**", "`_card`", "{\"card\": \"<ref>\"}", nodeGrammarExample} {
		if !strings.Contains(catalog, want) {
			t.Errorf("catalog_read lacks %q", want)
		}
	}
	// The components another one replaced are never offered: a node that
	// has them still reads (TestLegacyBlocksStillRender).
	for _, gone := range []string{"**List**", "**Fold**", "**Item**", "**Grid**", "**Card**", "**Button**", "**Metric**", "**Progress**", "[_group="} {
		if strings.Contains(catalog, gone) {
			t.Errorf("catalog_read offers %s", gone)
		}
	}
}
