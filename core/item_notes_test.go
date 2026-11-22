package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// An item's note is kept by the node, its source and the item; the
// source is one of the node's; no note nor state takes it away.
func TestNoteItem(t *testing.T) {
	c := graphCore(t)
	block := "\n```eriphe:block mails\n{\"components\": {\"component\": \"Text\", \"text\": \"Mails\"}, \"sources\": {\"inbox\": {\"tool\": \"mail/list\", \"every\": 5}}}\n```\n"
	n1 := mustNode(t, c, nodestore.New{Title: "Mails", Body: block})
	n2 := mustNode(t, c, nodestore.New{Title: "Plain", Body: "No source."})
	if err := c.noteItem(n1, itemNote{Source: "inbox", Item: "m1", Note: "Sam asks for Tuesday", State: "act", Author: "task:t1", Tainted: "task:t1"}); err != nil {
		t.Fatal(err)
	}
	items, _ := c.nodeItems(n1)
	if len(items) != 1 || items[0].Note != "Sam asks for Tuesday" || items[0].Tainted != "task:t1" {
		t.Fatalf("items: %+v", items)
	}
	for _, bad := range []struct {
		id string
		it itemNote
	}{
		{n1, itemNote{Source: "outbox", Item: "m1", Note: "x", Author: "brain"}},
		{n2, itemNote{Source: "inbox", Item: "m1", Note: "x", Author: "brain"}},
		{n1, itemNote{Source: "inbox", Item: "m1", State: "urgent", Author: "brain"}},
		{n1, itemNote{Source: "inbox", Item: "m1", Note: strings.Repeat("x", itemNoteMax+1), Author: "brain"}},
		{n1, itemNote{Source: "inbox", Item: "m1", Note: "token ghp_0123456789abcdefghijABCDEFGHIJ", Author: "brain"}},
		{n1, itemNote{Source: "inbox", Item: "", Note: "x", Author: "brain"}},
		{n1, itemNote{Source: "inbox", Item: "m1", Note: "x"}},
	} {
		if err := c.noteItem(bad.id, bad.it); err == nil {
			t.Errorf("noted: %s %+v", bad.id, bad.it)
		}
	}
	// A group alone is a note: the section a view shows the item in.
	if err := c.noteItem(n1, itemNote{Source: "inbox", Item: "m2", Group: "  À supprimer ", Author: "brain"}); err != nil {
		t.Fatal(err)
	}
	if items, _ := c.nodeItems(n1); len(items) != 2 || items[1].Group != "À supprimer" || items[1].Note != "" {
		t.Fatalf("grouped: %+v", items)
	}
	for _, g := range []string{strings.Repeat("x", itemGroupMax+1), "a]b", "a/b", "a=b", "a!b", "two\nlines"} {
		if err := c.noteItem(n1, itemNote{Source: "inbox", Item: "m2", Group: g, Author: "brain"}); err == nil {
			t.Errorf("group %q kept", g)
		}
	}
	for _, m := range []string{"m1", "m2"} {
		if err := c.noteItem(n1, itemNote{Source: "inbox", Item: m, Author: "brain"}); err != nil {
			t.Fatal(err)
		}
	}
	if items, _ := c.nodeItems(n1); len(items) != 0 {
		t.Fatalf("not taken away: %+v", items)
	}
	// An archived node is noted no more.
	if err := c.nodes.Archive(n1, byUser); err != nil {
		t.Fatal(err)
	}
	if err := c.noteItem(n1, itemNote{Source: "inbox", Item: "m3", Note: "x", Author: "brain"}); err == nil {
		t.Error("an archived node noted")
	}
}

// nodes.db is a pure index: deleted, it is made again from the files at
// the next start, and nothing is lost. The items' notes are in core.db,
// the traces beside the repository.
func TestIndexRebuiltLosesNothing(t *testing.T) {
	c := startCore(t)
	dir := t.TempDir()
	repo, index := filepath.Join(dir, "nodes"), filepath.Join(dir, "nodes.db")
	open := func() *nodestore.Store {
		s, _, err := nodestore.Open(repo, index, nodestore.Options{Refuse: refuseSecret})
		if err != nil {
			t.Fatal(err)
		}
		c.useNodes(s)
		return s
	}
	s := open()
	block := "\n```eriphe:block mails\n{\"components\": {\"component\": \"Text\", \"text\": \"Mails\"}, \"sources\": {\"inbox\": {\"tool\": \"mail/list\"}}}\n```\n"
	id := mustNode(t, c, nodestore.New{Title: "Mails", Body: "Les mails." + block})
	child := mustNode(t, c, nodestore.New{Title: "Réponse", Parent: id, Body: "Voir [[Mails]]."})
	if err := c.noteItem(id, itemNote{Source: "inbox", Item: "m1", Note: "Sam attend", State: "act", Group: "À traiter", Author: byBrain}); err != nil {
		t.Fatal(err)
	}
	if err := c.nodes.AppendTrace(id, byBrain, "Trié les mails."); err != nil {
		t.Fatal(err)
	}
	before, _ := c.nodes.Read(id, 0)
	s.Close()
	for _, f := range []string{index, index + "-wal", index + "-shm"} {
		os.Remove(f)
	}
	s = open()
	defer s.Close()
	after, err := c.nodes.Read(id, 0)
	if err != nil || after.Version.Hash != before.Version.Hash || string(after.Raw) != string(before.Raw) {
		t.Fatalf("rebuilt: %+v %v", after, err)
	}
	if sum, _ := c.nodes.Get(child); sum.Parent != id || len(sum.Links) != 1 || sum.Links[0] != id {
		t.Fatalf("child %+v", sum)
	}
	if items, _ := c.nodeItems(id); len(items) != 1 || items[0].Note != "Sam attend" || items[0].Group != "À traiter" || items[0].State != "act" {
		t.Fatalf("items %+v", items)
	}
	if tr, _ := c.nodes.Trace(id); !strings.Contains(string(tr), "Trié les mails.") {
		t.Fatalf("trace %q", tr)
	}
	if srcs, _ := c.nodes.Sources(id); len(srcs) != 1 {
		t.Fatalf("sources %+v", srcs)
	}
}
