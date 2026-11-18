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
	if got.Node.Meta.Done.IsZero() || got.State != Done || got.Version.N != 2 {
		t.Fatalf("not done: %+v %s", got.Node.Meta, got.State)
	}
	if changed, _ := s.SetDone("n1", true, "brain"); changed {
		t.Fatal("done twice")
	}
	// A write made on the version before it applies: done is code's.
	if _, err := s.Write("n1", "Draft, v2.", d.Version.Hash, "brain"); err != nil {
		t.Fatalf("an author's write after done: %v", err)
	}
	if changed, err := s.SetDone("n1", false, "user"); err != nil || !changed {
		t.Fatalf("not done again: %v %v", changed, err)
	}
	if got, _ := s.Get("n1"); !got.Done.IsZero() || got.State != Idle {
		t.Fatalf("still done: %+v", got)
	}
}

func TestLink(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	mustCreate(t, s, New{Title: "Mail from Sam", Body: "Triage."})
	mustCreate(t, s, New{Title: "Production", Body: "Errors."})
	if changed, err := s.Link("n1", "n2", "brain"); err != nil || !changed {
		t.Fatalf("link: %v %v", changed, err)
	}
	if changed, _ := s.Link("n1", "n2", "brain"); changed {
		t.Fatal("linked twice")
	}
	if _, err := s.Link("n1", "n9", "brain"); err == nil {
		t.Fatal("a link to no node")
	}
	if _, err := s.Link("n1", "n1", "brain"); err == nil {
		t.Fatal("a link to itself")
	}
	d, _ := s.Read("n1", 0)
	if len(d.Node.Meta.Links) != 1 || d.Node.Meta.Links[0] != "n2" {
		t.Fatalf("links: %v", d.Node.Meta.Links)
	}
}

func TestInStatesAndRecent(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	mustCreate(t, s, New{Title: "Style", Body: "Reading."})
	mustCreate(t, s, New{Title: "Plan", Body: "Waiting."})
	mustCreate(t, s, New{Title: "Old", Body: "Gone."})
	s.SetState("n1", Running)
	s.SetState("n2", Waiting)
	s.SetState("n3", Waiting)
	if err := s.Archive("n3", "user"); err != nil {
		t.Fatal(err)
	}
	got, err := s.InStates([]string{Running, Waiting}, 0)
	if err != nil || len(got) != 2 {
		t.Fatalf("in states: %+v %v", got, err)
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
