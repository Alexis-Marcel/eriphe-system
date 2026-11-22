package nodestore

import (
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
