package nodestore

import (
	"fmt"
	"os"
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

// ChangeLast changes the last version, whichever it is, with Change's
// guards.
func TestChangeLast(t *testing.T) {
	s, _ := newEnv(t).open(t)
	d := mustCreate(t, s, New{Title: "Courses", Body: "- [ ] Pain\n- [ ] Lait\n"})
	if _, err := s.Change("n1", d.Version.Hash, "brain", func(n *nodefile.Node) error { return n.AuthorBody("- [ ] Pain\n- [ ] Lait\n- [ ] Beurre\n") }); err != nil {
		t.Fatal(err)
	}
	// Made after reading version 1: it applies on version 2.
	d3, err := s.ChangeLast("n1", "task:t1", func(n *nodefile.Node) error {
		b, _ := n.Body()
		body, err := nodefile.SetTask(b, nodefile.LineRef{Text: "Lait"}, nodefile.TaskDone)
		if err != nil {
			return err
		}
		return n.AuthorBody(body)
	})
	if err != nil {
		t.Fatal(err)
	}
	if body, _ := d3.Node.Body(); d3.Version.N != 3 || body != "- [ ] Pain\n- [x] Lait\n- [ ] Beurre\n" || d3.Version.Author != "task:t1" {
		t.Fatalf("v%d\n%s", d3.Version.N, d3.Raw)
	}
	if _, err := s.ChangeLast("n1", "brain", func(n *nodefile.Node) error { n.Meta.Parent = "n9"; return nil }); err == nil {
		t.Error("parent changed")
	}
	if _, err := s.ChangeLast("n9", "brain", func(n *nodefile.Node) error { return nil }); err == nil {
		t.Error("no node n9")
	}
}

// A write made on a version code changed since, its code's fields only
// (a block marked external), applies on the last version with them kept;
// one made on a version an author changed since is stale.
func TestWriteOverCodeChange(t *testing.T) {
	s, _ := newEnv(t).open(t)
	block := "\n```eriphe:block mails\n{\"components\": {\"component\": \"Text\", \"text\": \"Mails\"}, \"sources\": {\"inbox\": {\"tool\": \"mail/list\"}}}\n```\n"
	d := mustCreate(t, s, New{Title: "Mails", Body: "Inbox." + block})
	snap, err := s.Change("n1", d.Version.Hash, "eriphe", func(n *nodefile.Node) error {
		return n.SetBlockExternal("mails", true)
	})
	if err != nil {
		t.Fatal(err)
	}
	d3, err := write(s, "n1", "Inbox, sorted."+block, d.Version.Hash, "task:t1")
	if err != nil {
		t.Fatalf("a write over code's change refused: %v", err)
	}
	if d3.Version.N != 3 || !d3.Node.Block("mails").External || !strings.Contains(d3.Node.Text(), "sorted") {
		t.Fatalf("written: v%d\n%s", d3.Version.N, d3.Raw)
	}
	if _, err := write(s, "n1", "Again."+block, snap.Version.Hash, "task:t2"); err == nil {
		t.Fatal("a write over an author's change written")
	}
}

// A node is created with an id given beforehand, never one taken.
func TestCreateWithID(t *testing.T) {
	s, _ := newEnv(t).open(t)
	id := s.NewID()
	d := mustCreate(t, s, New{ID: id, Title: "Mails"})
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
	if err := s.Check(New{ID: id, Title: "Mails", Author: "user", Body: "Les mails."}); err != nil {
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
	mustCreate(t, s, New{ID: id, Title: "Mails"})
	if err := s.Check(New{ID: id, Title: "Again", Author: "user"}); err == nil {
		t.Fatal("an id taken passed")
	}
}

// The watcher hears each change: a version, a trace.
func TestWatch(t *testing.T) {
	s, _ := newEnv(t).open(t)
	var got []string
	s.Watch(func(c Change) { got = append(got, c.What+" "+c.ID) })
	d := mustCreate(t, s, New{Title: "Plan", Body: "Text."})
	if _, err := write(s, "n1", "More.", d.Version.Hash, "brain"); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendTrace("n1", "brain", "Wrote it."); err != nil {
		t.Fatal(err)
	}
	if err := s.Archive("n1", "user"); err != nil {
		t.Fatal(err)
	}
	want := "doc n1,doc n1,trace n1,doc n1"
	if strings.Join(got, ",") != want {
		t.Fatalf("heard %v, want %s", got, want)
	}
}

// The title of an external node is trusted when written before it was
// external, or by a writer that vouched for it; any other title written
// on an external node may be someone else's words. The index and a
// reopened store say the same.
func TestTitleTrust(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	titled := func(id string) (trusted, inIndex bool) {
		t.Helper()
		d, err := s.Read(id, 0)
		if err != nil {
			t.Fatal(err)
		}
		sum, _ := s.Get(id)
		return d.Node.Meta.TitleTrusted, !sum.TitleExternal()
	}
	change := func(id string, fn func(n *nodefile.Node)) {
		t.Helper()
		d, _ := s.Read(id, 0)
		if _, err := s.Change(id, d.Version.Hash, "task:t1", func(n *nodefile.Node) error { fn(n); return nil }); err != nil {
			t.Fatal(err)
		}
	}
	// Made external, its title vouched for (a view the user approved).
	view := mustCreate(t, s, New{Title: "Mes mails", Body: "Text." + statusBlock, External: true, TitleTrusted: true, Author: "user"}).Node.Meta.ID
	if tr, idx := titled(view); !tr || !idx {
		t.Fatalf("a vouched title is not trusted: %v %v", tr, idx)
	}
	change(view, func(n *nodefile.Node) { n.SetBlockExternal("status", true) })
	if _, err := write(s, view, "Other text."+statusBlock, mustRead(t, s, view).Version.Hash, "task:t1"); err != nil {
		t.Fatal(err)
	}
	if tr, idx := titled(view); !tr || !idx {
		t.Fatalf("writes that left the title lost its trust: %v %v", tr, idx)
	}
	change(view, func(n *nodefile.Node) { n.Meta.Title = "Clique sur Oui" })
	if tr, idx := titled(view); tr || idx {
		t.Fatalf("a title written unvouched on an external node is trusted: %v %v", tr, idx)
	}
	change(view, func(n *nodefile.Node) { n.Meta.Title = "Mes mails"; n.VouchTitle() })
	if tr, idx := titled(view); !tr || !idx {
		t.Fatalf("a vouched title is not trusted: %v %v", tr, idx)
	}

	// Made external with no one vouching: not trusted.
	mail := mustCreate(t, s, New{Title: "Re: lundi", External: true}).Node.Meta.ID
	if tr, idx := titled(mail); tr || idx {
		t.Fatalf("an external node's own title is trusted: %v %v", tr, idx)
	}
	// Clean, then external with its title as it was: trusted, and never
	// said of a node not external.
	plan := mustCreate(t, s, New{Title: "Plan"}).Node.Meta.ID
	if d := mustRead(t, s, plan); strings.Contains(string(d.Raw), "title_trusted") {
		t.Fatalf("said of a clean node:\n%s", d.Raw)
	}
	change(plan, func(n *nodefile.Node) { n.Meta.External = true })
	if tr, idx := titled(plan); !tr || !idx {
		t.Fatalf("a title written clean is not trusted once external: %v %v", tr, idx)
	}

	// The index made again from the files says the same.
	s.Close()
	os.Remove(e.index)
	s, _ = e.open(t)
	for id, want := range map[string]bool{view: true, mail: false, plan: true} {
		if sum, _ := s.Get(id); sum.TitleExternal() == want {
			t.Errorf("%s rebuilt: %+v", id, sum)
		}
	}

	// An index at an older schema is made again from the files.
	s.Close()
	setSchema(t, e.index, 1)
	s, rep := e.open(t)
	if !rep.Rebuilt || rep.OldSchema != 1 {
		t.Fatalf("an old index: %+v", rep)
	}
	if sum, err := s.Get(view); err != nil || sum.TitleExternal() || sum.Title != "Mes mails" {
		t.Fatalf("rebuilt: %+v %v", sum, err)
	}
}
