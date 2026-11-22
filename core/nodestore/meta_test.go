package nodestore

import (
	"errors"
	"os"
	"slices"
	"testing"
)

func TestSetDone(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	d := mustCreate(t, s, New{Title: "Plan", Body: "Draft."})
	changed, err := s.SetDone("n1", true, "brain")
	if err != nil || !changed {
		t.Fatalf("done: %v %v", changed, err)
	}
	got, _ := s.Read("n1", 0)
	if got.Node.Meta.Done.IsZero() || got.Version.N != 2 {
		t.Fatalf("not done: %+v", got.Node.Meta)
	}
	if changed, _ := s.SetDone("n1", true, "brain"); changed {
		t.Fatal("done twice")
	}
	// A write made on the version before it applies: done is code's.
	if _, err := write(s, "n1", "Draft, v2.", d.Version.Hash, "brain"); err != nil {
		t.Fatalf("an author's write after done: %v", err)
	}
	if changed, err := s.SetDone("n1", false, "user"); err != nil || !changed {
		t.Fatalf("not done again: %v %v", changed, err)
	}
	if got, _ := s.Get("n1"); !got.Done.IsZero() {
		t.Fatalf("still done: %+v", got)
	}
}

// A node moves under another, or to the top, never under itself nor a
// node below it; the move is in its file: a rebuilt index keeps it.
func TestSetParent(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	mustCreate(t, s, New{Title: "Maison"})                // n1
	mustCreate(t, s, New{Title: "Travaux", Parent: "n1"}) // n2
	mustCreate(t, s, New{Title: "Devis", Parent: "n2"})   // n3
	mustCreate(t, s, New{Title: "Vacances"})              // n4
	for _, bad := range []string{"n1", "n3"} {
		if _, err := s.SetParent("n1", bad, "brain"); !errors.Is(err, ErrCycle) {
			t.Errorf("n1 under %s: %v", bad, err)
		}
	}
	if _, err := s.SetParent("n3", "nothere", "brain"); !errors.Is(err, ErrNotFound) {
		t.Errorf("under a missing node: %v", err)
	}
	if changed, err := s.SetParent("n3", "n4", "brain"); err != nil || !changed {
		t.Fatalf("move: %v %v", changed, err)
	}
	if changed, _ := s.SetParent("n3", "n4", "brain"); changed {
		t.Error("moved twice")
	}
	if kids, _ := s.Children("n4"); len(kids) != 1 || kids[0].ID != "n3" {
		t.Errorf("children of n4: %+v", kids)
	}
	if changed, err := s.SetParent("n2", "", "brain"); err != nil || !changed {
		t.Fatalf("to the top: %v %v", changed, err)
	}
	if err := s.Archive("n1", "user"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetParent("n3", "n1", "brain"); !errors.Is(err, ErrArchived) {
		t.Errorf("under an archived node: %v", err)
	}
	s.Close()
	for _, f := range []string{e.index, e.index + "-wal", e.index + "-shm"} {
		os.Remove(f)
	}
	s2, _ := e.open(t)
	if got, _ := s2.Get("n3"); got.Parent != "n4" {
		t.Errorf("rebuilt: n3 under %q", got.Parent)
	}
	if got, _ := s2.Get("n2"); got.Parent != "" {
		t.Errorf("rebuilt: n2 under %q", got.Parent)
	}
}

// The nodes that link a node, by their text or by code, live ones only.
func TestBacklinks(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	mustCreate(t, s, New{Title: "Sujet"})                          // n1
	mustCreate(t, s, New{Title: "Note", Body: "Voir [[Sujet]]."})  // n2
	mustCreate(t, s, New{Title: "Lien", Links: []string{"n1"}})    // n3
	mustCreate(t, s, New{Title: "Vieux", Body: "Voir [[Sujet]]."}) // n4
	mustCreate(t, s, New{Title: "Seul", Body: "Rien."})            // n5
	if err := s.Archive("n4", "user"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Backlinks("n1")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, x := range got {
		ids = append(ids, x.ID)
	}
	if !slices.Equal(ids, []string{"n2", "n3"}) {
		t.Errorf("backlinks %v", ids)
	}
}

func TestRecent(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	mustCreate(t, s, New{Title: "Style", Body: "Reading."})
	mustCreate(t, s, New{Title: "Plan", Body: "Waiting."})
	mustCreate(t, s, New{Title: "Old", Body: "Gone."})
	if err := s.Archive("n3", "user"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Recent(false, 0); len(got) != 2 {
		t.Fatalf("recent: %+v", got)
	}
	if got, _ := s.Recent(true, 0); len(got) != 3 || got[0].ID != "n3" {
		t.Fatalf("recent, archived too: %+v", got)
	}
	ids, _ := s.Below("n1")
	if len(ids) != 1 {
		t.Fatalf("below: %v", ids)
	}
}
