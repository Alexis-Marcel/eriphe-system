package main

import (
	"strings"
	"testing"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// A view's page says what keeps it alive, in code's words: the monitor
// its skill runs on and what it watches, the skill and what starts it,
// when it last changed. A node
// that is not standing says nothing of it.
func TestPageSaysWhatKeepsAViewAlive(t *testing.T) {
	c, node, _ := mailView(t)
	w := originWeb(t, c)
	p, err := w.nodePageOf(node, 0, "fr")
	if err != nil {
		t.Fatal(err)
	}
	a := p.Alive
	if a == nil || a.Updated.IsZero() {
		t.Fatalf("alive %+v", a)
	}
	if len(a.Monitors) != 1 || a.Monitors[0].Name != "mails" || !strings.Contains(a.Monitors[0].What, "monitor monitor://received") {
		t.Errorf("monitors %+v", a.Monitors)
	}
	if len(a.Skills) != 1 || a.Skills[0].Name != "tri-mails" || a.Skills[0].What != "lancé quand la surveillance « mails » voit quelque chose" {
		t.Errorf("skills %+v", a.Skills)
	}
	// In English too.
	if p, _ := w.nodePageOf(node, 0, "en"); p.Alive == nil || p.Alive.Skills[0].What != "started when the monitor “mails” sees something" {
		t.Errorf("en %+v", p.Alive)
	}

	work, err := c.nodes.Create(nodestore.New{Title: "Un travail", Author: byBrain})
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := w.nodePageOf(work.Node.Meta.ID, 0, "fr"); p.Alive != nil {
		t.Errorf("a work node is alive: %+v", p.Alive)
	}
}
