package main

import (
	"net/http"
	"slices"
	"strings"
	"testing"
)

func openedNow(fm *fakeMonitor) []string {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	return slices.Clone(fm.opened)
}

// A tool the user never allows opens nothing: the open asks no yes of
// its own, and runs no call.
func TestItemOpenPolicy(t *testing.T) {
	c, fm, node := refsCore(t)
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	c.pool.sync(setServer(c, "monitor", func(s *Server) { s.Exceptions = map[string]string{"message": gridNever} }))
	waitStatus(t, c.pool.get("monitor"), statusReady)
	if code, out := gesture(t, tw, csrf, "POST", "/api/nodes/"+node+"/open", itemOpenIn{Ref: "m1"}); code != http.StatusForbidden || out["title"] != nil {
		t.Fatalf("never: %d %v", code, out)
	}
	if got := openedNow(fm); len(got) != 0 {
		t.Fatalf("calls: %v", got)
	}
}

// What an item says: the fields its read tool declares (in it or in its
// one object); else a title and a sender as a row finds them, the whole
// under Détails; a text is its body.
func TestOpenFields(t *testing.T) {
	if o := openFields("Bonjour", toolOpen{}); o.Body != "Bonjour" || o.Raw != "" {
		t.Fatalf("a text: %+v", o)
	}
	event := map[string]any{"title": "Point", "organizer": map[string]any{"name": "Sam"}, "start": "10:00", "description": "Ordre du jour"}
	o := openFields(event, toolOpen{Date: "start", Body: "description"})
	if o.Title != "Point" || o.From != "Sam" || o.Date != "10:00" || o.Body != "Ordre du jour" {
		t.Fatalf("an event: %+v", o)
	}
	// Declared nothing: no field guessed by its name but a row's.
	o = openFields(map[string]any{"event": event}, toolOpen{})
	if o.Title != "Point" || o.From != "Sam" || o.Date != "" || o.Body != "" || !strings.Contains(o.Raw, "Ordre du jour") {
		t.Fatalf("undeclared: %+v", o)
	}
	o = openFields(map[string]any{"heading": "Une note", "when": "lundi", "words": "Le texte"}, toolOpen{Title: "heading", Date: "when", Body: "words"})
	if o.Title != "Une note" || o.Date != "lundi" || o.Body != "Le texte" {
		t.Fatalf("declared: %+v", o)
	}
	if o := openFields([]any{1, 2}, toolOpen{}); o.Title != "" || o.Raw == "" {
		t.Fatalf("a list: %+v", o)
	}
}
