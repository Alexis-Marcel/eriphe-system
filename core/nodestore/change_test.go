package nodestore

import (
	"strings"
	"testing"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
)

// A node created external holds external blocks.
func TestCreateExternal(t *testing.T) {
	s, _ := newEnv(t).open(t)
	d := mustCreate(t, s, New{Title: "Mail", Body: "From Sam." + statusBlock, External: true})
	if !d.Node.Meta.External || !d.Node.Block("status").External {
		t.Fatalf("not external:\n%s", d.Raw)
	}
}

// Code's change: code's fields and the title change, made on the last
// version; the id, parent, creation, done and archived never do; the
// filter runs over what is written.
func TestChange(t *testing.T) {
	s, _ := newEnv(t).open(t)
	d := mustCreate(t, s, New{Title: "Plan", Body: "Text." + statusBlock})
	d2, err := s.Change("n1", d.Version.Hash, "task:t1", func(n *nodefile.Node) error {
		n.Meta.External = true
		n.Meta.Title = "Le plan"
		return n.SetBlockExternal("status", true)
	})
	if err != nil {
		t.Fatal(err)
	}
	if d2.Version.N != 2 || !d2.Node.Meta.External || !d2.Node.Block("status").External || d2.Version.Author != "task:t1" {
		t.Fatalf("changed: v%d\n%s", d2.Version.N, d2.Raw)
	}
	if sum, _ := s.Get("n1"); !sum.External || sum.Title != "Le plan" {
		t.Fatalf("index: %+v", sum)
	}
	if _, err := s.Change("n1", d.Version.Hash, "brain", func(n *nodefile.Node) error { return nil }); err == nil {
		t.Fatal("a stale change written")
	}
	for name, fn := range map[string]func(n *nodefile.Node){
		"id":     func(n *nodefile.Node) { n.Meta.ID = "n9" },
		"parent": func(n *nodefile.Node) { n.Meta.Parent = "n9" },
		"done":   func(n *nodefile.Node) { n.Meta.Done = n.Meta.Created },
	} {
		if _, err := s.Change("n1", d2.Version.Hash, "brain", func(n *nodefile.Node) error { fn(n); return nil }); err == nil {
			t.Errorf("%s changed", name)
		}
	}
	if _, err := s.Change("n1", d2.Version.Hash, "brain", func(n *nodefile.Node) error { n.Meta.Goal = "ghp_x"; return nil }); err == nil {
		t.Fatal("a secret written by a change")
	}
}

// An item's note is kept by the node, its source and the item; the
// source is one of the node's; no note nor state takes it away.
func TestNoteItem(t *testing.T) {
	s, _ := newEnv(t).open(t)
	block := "\n```eriphe:block mails\n{\"components\": {\"component\": \"Text\", \"text\": \"Mails\"}, \"sources\": {\"inbox\": {\"tool\": \"mail/list\", \"every\": 5}}}\n```\n"
	mustCreate(t, s, New{Title: "Mails", Body: block})
	mustCreate(t, s, New{Title: "Plain", Body: "No source."})
	if err := s.NoteItem("n1", Item{Source: "inbox", Item: "m1", Note: "Sam asks for Tuesday", State: "act", Author: "task:t1", Tainted: "task:t1"}); err != nil {
		t.Fatal(err)
	}
	items, _ := s.Items("n1")
	if len(items) != 1 || items[0].Note != "Sam asks for Tuesday" || items[0].Tainted != "task:t1" {
		t.Fatalf("items: %+v", items)
	}
	for _, bad := range []struct {
		id string
		it Item
	}{
		{"n1", Item{Source: "outbox", Item: "m1", Note: "x", Author: "brain"}},
		{"n2", Item{Source: "inbox", Item: "m1", Note: "x", Author: "brain"}},
		{"n1", Item{Source: "inbox", Item: "m1", State: "urgent", Author: "brain"}},
		{"n1", Item{Source: "inbox", Item: "m1", Note: strings.Repeat("x", ItemNoteMax+1), Author: "brain"}},
		{"n1", Item{Source: "inbox", Item: "m1", Note: "ghp_x", Author: "brain"}},
	} {
		if err := s.NoteItem(bad.id, bad.it); err == nil {
			t.Errorf("noted: %s %+v", bad.id, bad.it)
		}
	}
	if err := s.NoteItem("n1", Item{Source: "inbox", Item: "m1", Author: "brain"}); err != nil {
		t.Fatal(err)
	}
	if items, _ := s.Items("n1"); len(items) != 0 {
		t.Fatalf("not taken away: %+v", items)
	}
}

func TestCreateAfterSiblings(t *testing.T) {
	s, _ := newEnv(t).open(t)
	parent := mustCreate(t, s, New{Title: "Substack", Author: "brain"})
	style := mustCreate(t, s, New{Title: "Style", Parent: parent.Node.Meta.ID, Author: "brain"})
	plan := mustCreate(t, s, New{Title: "Plan", Parent: parent.Node.Meta.ID, After: []string{style.Node.Meta.ID}, Author: "brain"})
	if got := plan.Node.Meta.After; len(got) != 1 || got[0] != style.Node.Meta.ID {
		t.Fatalf("after %v", got)
	}
	other := mustCreate(t, s, New{Title: "Other", Author: "brain"})
	for _, nw := range []New{
		{Title: "Loose", Parent: parent.Node.Meta.ID, After: []string{other.Node.Meta.ID}, Author: "brain"},
		{Title: "Top", After: []string{style.Node.Meta.ID}, Author: "brain"},
		{Title: "Missing", Parent: parent.Node.Meta.ID, After: []string{"zz9"}, Author: "brain"},
	} {
		if _, err := s.Create(nw); err == nil {
			t.Errorf("%s: created", nw.Title)
		}
	}
	if _, err := s.Change(plan.Node.Meta.ID, plan.Version.Hash, "brain", func(n *nodefile.Node) error {
		n.Meta.After = nil
		return nil
	}); err == nil {
		t.Error("after changed by a write")
	}
}
