package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestTaskLabel(t *testing.T) {
	if got := (&TaskRow{Title: "Écrire le README", Prompt: "Dépôt cloné dans /work/repo"}).label(); got != "Écrire le README" {
		t.Errorf("with a title: %q", got)
	}
	if got := (&TaskRow{Prompt: "  Dépôt cloné\nPuis le reste"}).label(); got != "Dépôt cloné" {
		t.Errorf("without one: %q", got)
	}
}

func TestRunRequestTitle(t *testing.T) {
	k := &taskContract{Objective: "Le README est à jour"}
	for _, c := range []struct {
		r    runRequest
		want string
	}{
		{runRequest{Title: " Écrire\nle README ", Contract: k}, "Écrire le README"},
		{runRequest{Contract: k}, "Le README est à jour"},
		{runRequest{}, ""},
	} {
		if got := c.r.title(); got != c.want {
			t.Errorf("%+v: %q", c.r, got)
		}
	}
	if got := (runRequest{Title: strings.Repeat("x", taskTitleMax+9)}).title(); len([]rune(got)) > taskTitleMax+1 {
		t.Errorf("%d long", len(got))
	}
}

func TestTaskTitleIsKept(t *testing.T) {
	s, err := openStore(filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.insertTask(&TaskRow{ID: "t-1", Kind: "worker", Runtime: "claude-code", State: statePending, Title: "Écrire le README", Prompt: "p"}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.task("t-1"); err != nil || got.Title != "Écrire le README" {
		t.Errorf("%+v %v", got, err)
	}
}
