package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Threads near a matter refuse a new thread for it, listed, ended ones
// too: the opener takes the one that is it (given back, reopened, and
// given the alias) or says why this is another matter (logged).
func TestNearThreadsRefuse(t *testing.T) {
	c := stepsCore(t)
	velora, err := c.openThread(threadIn{Title: "Nom de marque Velora", Goal: "Un nom déposé.", Alias: "topic:brand-velora"}, nil, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.closeLight(velora.ID, "Déposé.", byBrain); err != nil {
		t.Fatal(err)
	}
	// Its alias's words are in the new one's title, and the new alias's
	// words in its title: refused either way, the ended thread listed.
	for _, in := range []threadIn{
		{Title: "Velora", Goal: "x", Alias: "topic:velora"},
		{Title: "Le dépôt de la marque", Goal: "x", Alias: "topic:brand-velora-sport"},
	} {
		_, _, err := c.openThreadOnce(in, nil, byBrain)
		var near *nearMatch
		if !errors.As(err, &near) || len(near.threads) != 1 || near.threads[0].ID != velora.ID {
			t.Fatalf("%s: %v", in.Alias, err)
		}
	}
	if _, isErr := callBrainTool(t, c, "open_thread", map[string]any{"title": "Velora", "goal": "x", "alias": "topic:velora"}); !isErr {
		t.Fatal("the brain opened a second thread for a matter")
	}
	// Nothing near: opened at once.
	garden, _, err := c.openThreadOnce(threadIn{Title: "Le potager", Goal: "Planté.", Alias: "project:potager"}, nil, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	// take is one of the threads listed, nothing else.
	if _, _, err := c.openThreadOnce(threadIn{Title: "Velora", Goal: "x", Alias: "topic:velora", Take: garden.ID}, nil, byBrain); err == nil {
		t.Fatal("took a thread that is not near")
	}
	// Taken in a turn that read external content: given back, the alias
	// stays free.
	s, note, err := c.openThreadOnce(threadIn{Title: "Velora", Goal: "x", Alias: "topic:velora", Take: velora.ID}, []string{"mail"}, byBrain)
	if err != nil || s.ID != velora.ID || !s.active() || !strings.Contains(note, "not given") {
		t.Fatalf("tainted take: %+v %q %v", s, note, err)
	}
	if _, ok := c.aliasThread("topic:velora"); ok {
		t.Fatal("a tainted turn gave an alias")
	}
	// Taken in a clean turn: given back, and the alias is the thread's.
	s, _, err = c.openThreadOnce(threadIn{Title: "Velora", Goal: "x", Alias: "topic:velora", Take: velora.ID}, nil, byBrain)
	if id, ok := c.aliasThread("topic:velora"); err != nil || s.ID != velora.ID || !ok || id != velora.ID {
		t.Fatalf("take: %+v %v, alias of %d", s, err, id)
	}
	// Another matter, said so: opened, why logged on it.
	shop, _, err := c.openThreadOnce(threadIn{Title: "Boutique Velora", Goal: "En ligne.", Alias: "project:boutique", NewBy: "la boutique, pas le nom"}, nil, byBrain)
	if err != nil || shop.ID == velora.ID {
		t.Fatalf("new_because: %+v %v", shop, err)
	}
	if !slices.ContainsFunc(c.threadLog(shop.ID, 10), func(l ThreadLine) bool {
		return strings.Contains(l.Text, "la boutique, pas le nom") && strings.Contains(l.Text, "Ouvert à côté")
	}) {
		t.Errorf("why not logged: %+v", c.threadLog(shop.ID, 10))
	}
	// An exact alias is that thread, as before: no refusal.
	if s, _, err := c.openThreadOnce(threadIn{Title: "Velora", Goal: "x", Alias: "topic:velora"}, nil, byBrain); err != nil || s.ID != velora.ID {
		t.Fatalf("exact alias: %+v %v", s, err)
	}
	// A message that names a thread that ended: the turn is about it, and
	// a task there is told to reopen it.
	if err := c.closeLight(garden.ID, "Planté.", byBrain); err != nil {
		t.Fatal(err)
	}
	c.turn.about(0)
	c.turn.name(c.named("Et le potager ?"))
	if a := c.turnAbout(); a != threadRef(garden.ID) {
		t.Fatalf("the turn is about %q", a)
	}
	if _, err := c.taskThread(); err == nil || !strings.Contains(err.Error(), "reopen_thread") {
		t.Errorf("a task about an ended thread: %v", err)
	}
}

// openThreadOnce is the only path that opens a thread: no other code
// calls openThread, so none goes around the matcher.
func TestOneOpeningPath(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "thread_tools.go" {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), ".openThread(") {
			t.Errorf("%s opens a thread around openThreadOnce", f)
		}
	}
}
