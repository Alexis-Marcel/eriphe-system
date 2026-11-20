package nodestore

import (
	"testing"
)

// What the user archived lists the top of each archive, with how many went
// with it; reopening brings back what one archive took, and the parents
// it needs, never a node whose parent is not on the graph.
func TestArchivesAndReopen(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	mustCreate(t, s, New{Title: "Mails", Body: "The mails."})
	mustCreate(t, s, New{Title: "Mail from Sam", Parent: "n1", Body: "Triage."})
	mustCreate(t, s, New{Title: "Research the error", Parent: "n2", Body: "Logs."})
	mustCreate(t, s, New{Title: "Reply", Parent: "n2", Body: "Draft."})
	if err := s.Archive("n4", "user"); err != nil { // the reply alone, first
		t.Fatal(err)
	}
	if err := s.Archive("n2", "user"); err != nil { // then the mail and the rest
		t.Fatal(err)
	}
	got, err := s.Archives(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "n2" || got[0].With != 1 || got[1].ID != "n4" || got[1].With != 0 {
		t.Fatalf("archives: %+v", got)
	}
	// The reply was archived on its own: reopened, its parent comes back,
	// not the research archived with the parent.
	if err := s.Reopen("n4", "user"); err != nil {
		t.Fatal(err)
	}
	live := map[string]bool{}
	xs, _ := s.Graph()
	for _, x := range xs {
		live[x.ID] = true
	}
	if !live["n4"] || !live["n2"] || live["n3"] {
		t.Fatalf("after reopening the reply: %v", live)
	}
	for _, x := range xs {
		if x.Parent != "" && !live[x.Parent] {
			t.Fatalf("%s is on the graph without its parent %s", x.ID, x.Parent)
		}
	}
	if d := mustRead(t, s, "n4"); !d.Node.Meta.Archived.IsZero() || d.Version.N != 3 {
		t.Fatalf("n4 after reopen: v%d archived %v", d.Version.N, d.Node.Meta.Archived)
	}
	// Reopening what is on the graph does nothing.
	if err := s.Reopen("n1", "user"); err != nil {
		t.Fatal(err)
	}
	if err := s.Reopen("n3", "user"); err != nil {
		t.Fatal(err)
	}
	if xs, _ := s.Graph(); len(xs) != 4 {
		t.Fatalf("graph: %d nodes", len(xs))
	}
}
