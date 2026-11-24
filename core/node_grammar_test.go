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
	if bad := listsOK(b); len(bad) > 0 {
		t.Fatalf("its sections: %v", bad)
	}
	c := graphCore(t)
	out := mustBrainNodes(t, c, "catalog_read", map[string]any{})
	catalog, _ := out["catalog"].(string)
	for _, want := range []string{"## Composants", "# Un bloc d'Eriphe", "`glance`", "`\"open\"", "[_group=", "`_card`", "{\"card\": \"<ref>\"}", nodeGrammarExample} {
		if !strings.Contains(catalog, want) {
			t.Errorf("catalog_read lacks %q", want)
		}
	}
}
