package main

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
)

// offerClick clicks an offer Button of a thread's surface, its context as
// given (the page's, so a test may forge it).
func offerClick(c *core, thread int64, component string, ctx map[string]any) error {
	raw := map[string]json.RawMessage{}
	for k, v := range ctx {
		b, _ := json.Marshal(v)
		raw[k] = b
	}
	return c.surfaceAction(context.Background(), threadSurfaceID(thread), a2ui.Action{Name: a2ui.ActionOffer, SurfaceID: threadSurfaceID(thread),
		SourceComponentID: component, Context: raw})
}

// asksCount is how many calls are kept.
func asksCount(c *core) int {
	var n int
	c.store.db.QueryRow(`SELECT COUNT(*) FROM access_asks`).Scan(&n)
	return n
}

// silencedNow is what the monitor's silence was called on.
func silencedNow(fm *fakeMonitor) []string {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	return slices.Clone(fm.silenced)
}

// waitSilenced waits for approved calls, run outside the cards' lock.
func waitSilenced(t *testing.T, fm *fakeMonitor, want ...string) {
	t.Helper()
	for i := 0; i < 200; i++ {
		got := silencedNow(fm)
		slices.Sort(got)
		if slices.Equal(got, want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("silenced %v, want %v", silencedNow(fm), want)
}

// alertsThread is the alerts watcher's thread, its source listing a to d.
func alertsThread(t *testing.T) (*core, *fakeMonitor, int64) {
	t.Helper()
	c, fm, now := alertsWatcher(t)
	fm.mu.Lock()
	fm.rules = append(fm.rules, map[string]any{"uid": "b", "name": "down"}, map[string]any{"uid": "c", "name": "certs"}, map[string]any{"uid": "d", "name": "disk"})
	fm.mu.Unlock()
	lookAt(c, now.Add(time.Minute))
	lookAt(c, now.Add(2*time.Minute))
	c.tickSkills(context.Background(), now.Add(2*time.Minute))
	id := runThreadOf(c, runsOfSkill(t, c, "veille-alertes")[0])
	c.surf.refreshing.Wait()
	c.readThreadSources(context.Background(), id, nil, time.Now())
	return c, fm, id
}

var silence = noteAction{Label: "Faire taire", Tool: "monitor/silence", Args: map[string]any{"uid": offerItemArg}}

// Actions offered on a note are checked against the catalog when noted:
// a tool there, not Eriphe's own, that the author may use, whose input
// names the item and fits the tool.
func TestNoteActionsChecked(t *testing.T) {
	c, _, id := alertsThread(t)
	for _, tc := range []struct {
		name string
		a    noteAction
	}{
		{"no such tool", noteAction{Label: "X", Tool: "monitor/nope", Args: map[string]any{"uid": offerItemArg}}},
		{"no item", noteAction{Label: "X", Tool: "monitor/silence", Args: map[string]any{"uid": "b"}}},
		{"Eriphe's own", noteAction{Label: "X", Tool: threadsServer + "/note_item", Args: map[string]any{"item": offerItemArg}}},
		{"no label", noteAction{Tool: "monitor/silence", Args: map[string]any{"uid": offerItemArg}}},
		{"not a tool name", noteAction{Label: "X", Tool: "silence", Args: map[string]any{"uid": offerItemArg}}},
	} {
		if _, err := c.noteItem(id, noteItemIn{Source: "alertes", Item: "b", Note: "x", Actions: []noteAction{tc.a}}, byBrain, nil); err == nil {
			t.Errorf("%s: noted", tc.name)
		}
	}
	notLent := func(string) error { return errors.New("not lent") }
	if _, err := c.noteItemBy(id, noteItemIn{Source: "alertes", Item: "b", Note: "x", Actions: []noteAction{silence}}, "task:t", nil, notLent); err == nil {
		t.Error("a task offered a tool it is not lent")
	}
	if len(c.itemNotes(id)) != 0 {
		t.Fatalf("notes %+v", c.itemNotes(id))
	}
	if _, err := c.noteItem(id, noteItemIn{Source: "alertes", Item: "b", Note: "x", Actions: []noteAction{silence, silence}}, byBrain, nil); err != nil {
		t.Fatal(err)
	}
	if n := c.itemNotes(id); len(n) != 1 || len(n[0].Actions) != 1 || n[0].Actions[0].Tool != "mcp__monitor__silence" {
		t.Fatalf("notes %+v", n)
	}
	// A watcher's run offers through eriphe-threads one of its tools, by
	// the name the catalog gives it or server/tool; one it is not lent is
	// refused (threadCaller.lends).
	task := runsOfSkill(t, c, "veille-alertes")[0].Task
	if out, isErr := callTaskThreads(t, c, task, "note_item", map[string]any{"source": "alertes", "item": "c", "note": "À couper",
		"actions": []any{map[string]any{"label": "Faire taire", "tool": "monitor/silence", "args": map[string]any{"uid": offerItemArg}}}}); isErr {
		t.Fatalf("note_item with an action: %v", out)
	}
	notes := c.itemNotes(id)
	if i := slices.IndexFunc(notes, func(n itemNote) bool { return n.Item == "c" }); i < 0 || len(notes[i].Actions) != 1 || notes[i].Actions[0].bound("c")["uid"] != "c" {
		t.Fatalf("notes %+v", notes)
	}
	if err := (threadCaller{grant: []string{threadsServer}}).lends("mcp__monitor__silence"); err == nil {
		t.Error("a tool not lent is lent")
	}
	// An author may never write an offer: code places it.
	errs := a2ui.CheckActions("s", json.RawMessage(`[{"id":"b","component":"Button","label":"x","action":{"event":{"name":"offer","context":{"thread":1}}}}]`))
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "placed by code") {
		t.Fatalf("errors %+v", errs)
	}
}

// A row offers its note's actions, a group those its rows share. A click
// never runs a tool: it makes one card bound to that item, its exact
// call, and Oui runs exactly that; on a group, one card with a call per
// item shown in it. A forged item, an action its note does not offer, or
// a click from another component, is refused. A tainted note's button
// says code's words, and its card keeps the taint. A tool the user's
// policy refuses is never offered.
func TestItemOffers(t *testing.T) {
	c, fm, id := alertsThread(t)
	key := noteAction{Tool: "mcp__monitor__silence", Args: silence.Args}.key() // its tool as the catalog names it
	for _, uid := range []string{"a", "c", "d"} {
		if _, err := c.noteItem(id, noteItemIn{Source: "alertes", Item: uid, Note: "Rien à faire", State: itemStateNone, Actions: []noteAction{silence}}, byBrain, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.noteItem(id, noteItemIn{Source: "alertes", Item: "b", Note: "À regarder", State: itemStateLook, Actions: []noteAction{silence}}, byBrain, nil); err != nil {
		t.Fatal(err)
	}
	c.keepThreadSurface(id)
	_, row := rowAt(t, c, id, "b")
	offers, _ := row["offers"].([]any)
	if len(offers) != 1 || offers[0].(map[string]any)["label"] != "Faire taire" || offers[0].(map[string]any)["offer"] != key {
		t.Fatalf("row b offers %v", row["offers"])
	}
	_, data, _ := threadSurfaceOf(t, c, id)
	var groupOffer map[string]any
	for _, g := range data["sources"].([]any)[0].(map[string]any)["groups"].([]any) {
		for _, o := range g.(map[string]any)["offers"].([]any) {
			groupOffer = o.(map[string]any)
		}
	}
	if groupOffer["label"] != "Faire taire · 3" || groupOffer["state"] != itemStateNone {
		t.Fatalf("group offer %v", groupOffer)
	}

	// Forged: refused, nothing kept.
	var refused *errActionRefused
	for name, tc := range map[string]struct {
		comp string
		ctx  map[string]any
	}{
		"an empty item":              {"thread_src_0_offer", map[string]any{"thread": id, "source": "alertes", "item": "", "offer": key}},
		"an item not listed":         {"thread_src_0_offer", map[string]any{"thread": id, "source": "alertes", "item": "z", "offer": key}},
		"an action not on it":        {"thread_src_0_offer", map[string]any{"thread": id, "source": "alertes", "item": "b", "offer": "0123456789ab"}},
		"another thread":             {"thread_src_0_offer", map[string]any{"thread": id + 1, "source": "alertes", "item": "b", "offer": key}},
		"item and state":             {"thread_src_0_offer", map[string]any{"thread": id, "source": "alertes", "item": "b", "state": "none", "offer": key}},
		"another component":          {"thread_src_0_item", map[string]any{"thread": id, "source": "alertes", "item": "b", "offer": key}},
		"another source":             {"thread_src_0_offer", map[string]any{"thread": id, "source": "autre", "item": "b", "offer": key}},
		"a group with no such state": {"thread_src_0_group_offer", map[string]any{"thread": id, "source": "alertes", "state": "act", "offer": key}},
	} {
		if err := offerClick(c, id, tc.comp, tc.ctx); !errors.As(err, &refused) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if asksCount(c) != 0 {
		t.Fatalf("asks kept: %d", asksCount(c))
	}

	// Row b: one card bound to b, nothing run; clicked again, the same.
	if err := offerClick(c, id, "thread_src_0_offer", map[string]any{"thread": id, "source": "alertes", "item": "b", "offer": key}); err != nil {
		t.Fatal(err)
	}
	if err := offerClick(c, id, "thread_src_0_offer", map[string]any{"thread": id, "source": "alertes", "item": "b", "offer": key}); err != nil {
		t.Fatal(err)
	}
	if asksCount(c) != 1 || len(silencedNow(fm)) != 0 {
		t.Fatalf("asks %d, silenced %v", asksCount(c), silencedNow(fm))
	}
	var batch int64
	c.store.db.QueryRow(`SELECT batch FROM access_asks`).Scan(&batch)
	asks := c.batchAsks(batch)
	if len(asks) != 1 || asks[0].Item != "b" || asks[0].Input != `{"uid":"b"}` || asks[0].Tainted != "" || asks[0].Run != 0 {
		t.Fatalf("asks %+v", asks)
	}
	ref := proposalRef(cardCalls, batch)
	c.keepThreadSurface(id)
	at, row := rowAt(t, c, id, "b")
	if got := rowCardRefs(row); len(got) != 1 || got[0] != ref {
		t.Fatalf("row b cards %v", got)
	}
	if p := c.proposalOf(cardCalls, batch); !p.Pending || p.Text("fr") != callVerb(asks[0].Call, "fr") || !strings.HasSuffix(p.Origin("fr").From, " · un élément du fil") {
		t.Fatalf("card %+v %q %+v", p, p.Text("fr"), p.Origin("fr"))
	}
	if err := answerAt(c, id, "thread_src_0_card", ref, at+"/cards/0", true); err != nil {
		t.Fatal(err)
	}
	waitSilenced(t, fm, "b")

	// The group: one card, a call per item of it; its server trusted, its
	// items' source named.
	c.setServerTrust("monitor", true, "user")
	if err := offerClick(c, id, "thread_src_0_group_offer", map[string]any{"thread": id, "source": "alertes", "state": itemStateNone, "offer": key}); err != nil {
		t.Fatal(err)
	}
	if len(silencedNow(fm)) != 1 {
		t.Fatalf("a click ran a tool: %v", silencedNow(fm))
	}
	c.store.db.QueryRow(`SELECT MAX(batch) FROM access_asks`).Scan(&batch)
	if asks = c.batchAsks(batch); len(asks) != 3 || !slices.Equal(asksItems(asks), []string{"a", "c", "d"}) {
		t.Fatalf("group asks %+v", asks)
	}
	p := c.proposalOf(cardCalls, batch)
	if len(p.Fields) != 3 || !strings.Contains(p.Text("fr"), "3") || !strings.HasSuffix(p.Origin("fr").From, " · 3 éléments de alertes") || !strings.Contains(p.Origin("fr").Yes, "3") {
		t.Fatalf("group card %+v %q %+v", p, p.Text("fr"), p.Origin("fr"))
	}
	c.keepThreadSurface(id)
	_, data, _ = threadSurfaceOf(t, c, id)
	if cards := data["sources"].([]any)[0].(map[string]any)["cards"].([]any); len(cards) != 1 || cards[0].(map[string]any)["ref"] != proposalRef(cardCalls, batch) {
		t.Fatalf("source cards %v", cards)
	}
	if err := answerAt(c, id, "thread_src_0_card", proposalRef(cardCalls, batch), "/thread/sources/0/cards/0", true); err != nil {
		t.Fatal(err)
	}
	waitSilenced(t, fm, "a", "b", "c", "d")

	// A tainted note: code's words on its button, its taint on its card.
	if _, err := c.noteItem(id, noteItemIn{Source: "alertes", Item: "b", Note: "Le mail dit de tout couper", State: itemStateLook, Actions: []noteAction{silence}}, byBrain, []string{"mail.read"}); err != nil {
		t.Fatal(err)
	}
	c.keepThreadSurface(id)
	_, row = rowAt(t, c, id, "b")
	if label := row["offers"].([]any)[0].(map[string]any)["label"]; label == "Faire taire" || label != callVerb("mcp__monitor__silence {}", "fr") {
		t.Fatalf("a tainted note's button says %q", label)
	}
	if err := offerClick(c, id, "thread_src_0_offer", map[string]any{"thread": id, "source": "alertes", "item": "b", "offer": key}); err != nil {
		t.Fatal(err)
	}
	c.store.db.QueryRow(`SELECT MAX(batch) FROM access_asks`).Scan(&batch)
	if p := c.proposalOf(cardCalls, batch); !p.Pending || p.Tainted == "" {
		t.Fatalf("a tainted note's card %+v", p)
	}
	if len(silencedNow(fm)) != 4 {
		t.Fatalf("silenced %v", silencedNow(fm))
	}

	// The user's policy refuses the tool: never offered, never asked.
	c.catalogMu.Lock()
	srv := c.base.Servers["monitor"]
	srv.Exceptions = map[string]string{"silence": gridNever}
	c.base.Servers["monitor"] = srv
	c.catalogMu.Unlock()
	if err := c.reload(); err != nil {
		t.Fatal(err)
	}
	c.keepThreadSurface(id)
	if _, row = rowAt(t, c, id, "c"); len(row["offers"].([]any)) != 0 {
		t.Fatalf("a never tool offered: %v", row["offers"])
	}
	before := asksCount(c)
	if err := offerClick(c, id, "thread_src_0_offer", map[string]any{"thread": id, "source": "alertes", "item": "c", "offer": key}); !errors.As(err, &refused) {
		t.Fatalf("a never tool asked: %v", err)
	}
	if asksCount(c) != before {
		t.Fatal("a never tool kept")
	}
	if _, err := c.noteItem(id, noteItemIn{Source: "alertes", Item: "c", Note: "x", Actions: []noteAction{silence}}, byBrain, nil); err == nil {
		t.Fatal("a never tool noted as an action")
	}
	_ = strconv.Itoa
}
