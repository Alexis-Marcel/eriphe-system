package main

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// openBlock is a block that lists the monitor's received messages, each
// opened by the tool given.
func openBlock(open string) string {
	return "```eriphe:block inbox\n" + `{"components": [
  {"id": "root", "component": "List", "children": {"path": "/received/messages", "componentId": "row"}},
  {"id": "row", "component": "Text", "text": {"@path": "subject"}}],
 "sources": {"received": {"resource": "monitor", "uri": "monitor://received", "items": {"path": "messages", "id": "id", "title": "subject", "open": ` + open + `}}}}` + "\n```\n"
}

// openWeb is the workspace on a core whose monitor lists m1 and m2, and
// a node that opens them by the tool given, read once.
func openWeb(t *testing.T, open string) (*testWeb, string, *fakeMonitor, string) {
	t.Helper()
	c, fm, _ := viewCore(t)
	fm.mu.Lock()
	fm.got = []map[string]any{{"id": "m1", "subject": "Facture", "from": "Sam <sam@example.org>"}, {"id": "m2", "subject": "Relance", "from": "Lou"}}
	fm.mu.Unlock()
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	id := mustNode(t, c, nodestore.New{Title: "Mails", Lifetime: nodefile.Standing, Body: "Les mails.\n"})
	if _, err := c.authorWrite(id, lastHash(t, c, id), byBrain, false, c.viewSourceOK(""), func(n *nodefile.Node) error {
		return n.AuthorBody("Les mails.\n\n" + openBlock(open))
	}); err != nil {
		t.Fatal(err)
	}
	c.readNode(context.Background(), id, nil, time.Now())
	return tw, csrf, fm, id
}

func openedNow(fm *fakeMonitor) []string {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	return slices.Clone(fm.opened)
}

// An open is checked when an author writes it: a read tool of the
// source's own server, its id parameter there, nothing else required.
func TestItemOpenChecked(t *testing.T) {
	c, _, _ := viewCore(t)
	id := mustNode(t, c, nodestore.New{Title: "Mails", Lifetime: nodefile.Standing, Body: "Les mails.\n"})
	write := func(open string) error {
		_, err := c.authorWrite(id, lastHash(t, c, id), byBrain, false, c.viewSourceOK(""), func(n *nodefile.Node) error {
			return n.AuthorBody("Les mails.\n\n" + openBlock(open))
		})
		return err
	}
	if err := write(`{"tool": "monitor/message", "id": "id"}`); err != nil {
		t.Fatalf("a good open: %v", err)
	}
	for want, open := range map[string]string{
		"not a tool of the catalog":  `{"tool": "monitor/nope", "id": "id"}`,
		"takes no uid":               `{"tool": "monitor/message", "id": "uid"}`,
		"is not a read":              `{"tool": "monitor/send", "id": "id"}`,
		"not a tool of the source's": `{"tool": "github/get_issue", "id": "id"}`,
		"items' open is":             `{"tool": "message", "id": "id"}`,
		"requires more than id":      `{"tool": "monitor/thread", "id": "id"}`,
	} {
		if err := write(open); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", open, err, want)
		}
	}
}

// The click opens an item the source lists, by its id as the source gave
// it: a free read runs, and its common fields come back with the whole;
// an id the source does not list runs nothing.
func TestItemOpenReads(t *testing.T) {
	tw, csrf, fm, id := openWeb(t, `{"tool": "monitor/message", "id": "id"}`)
	c := tw.c
	if err := c.noteItem(id, itemNote{Source: "received", Item: "m1", Group: "À supprimer", Note: "Une relance", Author: byBrain, Tainted: "external:monitor"}); err != nil {
		t.Fatal(err)
	}
	code, out := gesture(t, tw, csrf, "POST", "/api/nodes/"+id+"/open", itemOpenIn{Block: "inbox", Source: "received", Item: "m1"})
	if code != http.StatusOK || out["title"] != "Facture" || out["from"] != "Sam <sam@example.org>" || out["date"] != "2026-11-04T09:00:00Z" || out["body"] != "Le texte de m1" || !strings.Contains(out["raw"].(string), `"subject": "Facture"`) {
		t.Fatalf("open: %d %v", code, out)
	}
	if out["external"] != true {
		t.Fatalf("a server not trusted: %v", out)
	}
	note, _ := out["note"].(map[string]any)
	if note["group"] != "À supprimer" || note["note"] != "Une relance" || note["external"] != true {
		t.Fatalf("its note: %v", out["note"])
	}
	if got := openedNow(fm); !slices.Equal(got, []string{"message:m1"}) {
		t.Fatalf("calls: %v", got)
	}
	if code, _ := gesture(t, tw, csrf, "POST", "/api/nodes/"+id+"/open", itemOpenIn{Block: "inbox", Source: "received", Item: "m9"}); code != http.StatusBadRequest {
		t.Fatalf("an id not listed: %d", code)
	}
	if code, _ := gesture(t, tw, csrf, "POST", "/api/nodes/"+id+"/open", itemOpenIn{Block: "inbox", Source: "nope", Item: "m1"}); code != http.StatusConflict {
		t.Fatalf("a source that opens nothing: %d", code)
	}
	if got := openedNow(fm); len(got) != 1 {
		t.Fatalf("calls after the refusals: %v", got)
	}
	d, _ := c.nodes.Read(id, 0)
	if raw, _ := c.nodes.Trace(id); strings.Contains(string(d.Raw), "Le texte") || strings.Contains(string(raw), "Le texte") {
		t.Fatal("what was read was kept")
	}
	// The page knows the List opens its rows.
	page, _ := tw.w.nodePageOf(id, 0, "fr")
	if l := page.Parts[len(page.Parts)-1].Block.Lists; len(l) != 1 || !l[0].Open {
		t.Fatalf("lists: %+v", l)
	}
}

// A read that is not free ahead (it reaches the open web) asks the user
// first, the call in code's words, and runs on their yes; a tool never
// allowed runs nothing.
func TestItemOpenPolicy(t *testing.T) {
	tw, csrf, fm, id := openWeb(t, `{"tool": "monitor/letter", "id": "id"}`)
	c := tw.c
	code, out := gesture(t, tw, csrf, "POST", "/api/nodes/"+id+"/open", itemOpenIn{Block: "inbox", Source: "received", Item: "m2"})
	ask, _ := out["ask"].(map[string]any)
	if code != http.StatusOK || ask["tool"] != "monitor/letter" || out["body"] != nil {
		t.Fatalf("no yes yet: %d %v", code, out)
	}
	if got := openedNow(fm); len(got) != 0 {
		t.Fatalf("it ran before the yes: %v", got)
	}
	// A yes for another call than the one shown: nothing runs.
	if code, _ := gesture(t, tw, csrf, "POST", "/api/nodes/"+id+"/open", itemOpenIn{Block: "inbox", Source: "received", Item: "m2", Yes: true, Tool: "monitor/message"}); code != http.StatusConflict {
		t.Fatalf("a yes for another call: %d", code)
	}
	if got := openedNow(fm); len(got) != 0 {
		t.Fatalf("it ran on a yes for another call: %v", got)
	}
	code, out = gesture(t, tw, csrf, "POST", "/api/nodes/"+id+"/open", itemOpenIn{Block: "inbox", Source: "received", Item: "m2", Yes: true, Tool: "monitor/letter"})
	if code != http.StatusOK || out["body"] != "Le texte de m2" {
		t.Fatalf("on the yes: %d %v", code, out)
	}
	rows, _ := c.store.decisions(0, 5)
	if len(rows) == 0 || rows[0].By != byUser || !strings.Contains(rows[0].Reason, "dit oui") {
		t.Fatalf("journal: %+v", rows)
	}

	c.pool.sync(setServer(c, "monitor", func(s *Server) { s.Exceptions = map[string]string{"letter": gridNever} }))
	waitStatus(t, c.pool.get("monitor"), statusReady)
	if code, _ := gesture(t, tw, csrf, "POST", "/api/nodes/"+id+"/open", itemOpenIn{Block: "inbox", Source: "received", Item: "m2", Yes: true, Tool: "monitor/letter"}); code != http.StatusForbidden {
		t.Fatalf("never: %d", code)
	}
	if got := openedNow(fm); len(got) != 1 {
		t.Fatalf("calls: %v", got)
	}
}

// What an item says is picked generically: a text is its body; an object
// its common fields, in it or in its one object.
func TestOpenFields(t *testing.T) {
	if o := openFields("Bonjour"); o.Body != "Bonjour" || o.Raw != "" {
		t.Fatalf("a text: %+v", o)
	}
	o := openFields(map[string]any{"title": "Point", "organizer": map[string]any{"name": "Sam"}, "start": "10:00", "description": "Ordre du jour"})
	if o.Title != "Point" || o.From != "Sam" || o.Date != "10:00" || o.Body != "Ordre du jour" {
		t.Fatalf("an event: %+v", o)
	}
	if o := openFields([]any{1, 2}); o.Title != "" || o.Raw == "" {
		t.Fatalf("a list: %+v", o)
	}
}
