package nodestore

import (
	"fmt"
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

// A write made on a version code changed since, its code's fields only
// (a source's snapshot), applies on the last version with them kept; one
// made on a version an author changed since is stale.
func TestWriteOverCodeChange(t *testing.T) {
	s, _ := newEnv(t).open(t)
	block := "\n```eriphe:block mails\n{\"components\": {\"component\": \"Text\", \"text\": \"Mails\"}, \"sources\": {\"inbox\": {\"tool\": \"mail/list\"}}}\n```\n"
	d := mustCreate(t, s, New{Title: "Mails", Body: "Inbox." + block})
	snap, err := s.Change("n1", d.Version.Hash, "eriphe", func(n *nodefile.Node) error {
		return n.SetSnapshot("mails", []byte(`{"inbox": {"messages": []}}`))
	})
	if err != nil {
		t.Fatal(err)
	}
	d3, err := s.Write("n1", "Inbox, sorted."+block, d.Version.Hash, "task:t1")
	if err != nil {
		t.Fatalf("a write over a snapshot refused: %v", err)
	}
	if d3.Version.N != 3 || d3.Node.Block("mails").Snapshot == nil || !strings.Contains(d3.Node.Text(), "sorted") {
		t.Fatalf("written: v%d\n%s", d3.Version.N, d3.Raw)
	}
	if _, err := s.Write("n1", "Again."+block, snap.Version.Hash, "task:t2"); err == nil {
		t.Fatal("a write over an author's change written")
	}
}

// A node is created with an id given beforehand, never one taken.
func TestCreateWithID(t *testing.T) {
	s, _ := newEnv(t).open(t)
	id := s.NewID()
	d := mustCreate(t, s, New{ID: id, Title: "Mails", Lifetime: nodefile.Standing})
	if d.Node.Meta.ID != id {
		t.Fatalf("id %s, not %s", d.Node.Meta.ID, id)
	}
	if _, err := s.Create(New{ID: id, Title: "Again", Author: "brain"}); err == nil {
		t.Fatal("an id taken given again")
	}
	if _, err := s.Create(New{ID: "Not-an-id", Title: "Bad", Author: "brain"}); err == nil {
		t.Fatal("a bad id accepted")
	}
	if s.NewID() == id {
		t.Fatal("NewID gave a taken id")
	}
}

// Check says what Create would refuse, and makes nothing: a secret, too
// many blocks, an id taken.
func TestCheckMakesNothing(t *testing.T) {
	s, _ := newEnv(t).open(t)
	id := s.NewID()
	if err := s.Check(New{ID: id, Title: "Mails", Lifetime: nodefile.Standing, Author: "user", Body: "Les mails."}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(id); err == nil {
		t.Fatal("Check made the node")
	}
	if err := s.Check(New{Title: "Keys", Author: "user", Body: "ghp_0123456789abcdefghij"}); err == nil {
		t.Fatal("a secret passed")
	}
	var many strings.Builder
	for i := 0; i <= MaxBlocks; i++ {
		fmt.Fprintf(&many, "```eriphe:block b%d\n{\"components\": {\"component\": \"Text\", \"text\": \"x\"}}\n```\n\n", i)
	}
	if err := s.Check(New{Title: "Many", Author: "user", Body: many.String()}); err == nil || !strings.Contains(err.Error(), "blocks at most") {
		t.Fatalf("too many blocks: %v", err)
	}
	mustCreate(t, s, New{ID: id, Title: "Mails", Lifetime: nodefile.Standing})
	if err := s.Check(New{ID: id, Title: "Again", Author: "user"}); err == nil {
		t.Fatal("an id taken passed")
	}
}

// The watcher hears each change: a version, a trace, a state; a state set
// again to what it was is no change.
func TestWatch(t *testing.T) {
	s, _ := newEnv(t).open(t)
	var got []string
	s.Watch(func(c Change) { got = append(got, c.What+" "+c.ID) })
	d := mustCreate(t, s, New{Title: "Plan", Body: "Text."})
	if _, err := s.Write("n1", "More.", d.Version.Hash, "brain"); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendTrace("n1", "brain", "Wrote it."); err != nil {
		t.Fatal(err)
	}
	s.SetState("n1", Running)
	s.SetState("n1", Running)
	if err := s.Archive("n1", "user"); err != nil {
		t.Fatal(err)
	}
	want := "doc n1,doc n1,trace n1,state n1,doc n1"
	if strings.Join(got, ",") != want {
		t.Fatalf("heard %v, want %s", got, want)
	}
}
