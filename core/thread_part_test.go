package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

// partRoots are the roots of a thread's part's blocks, as its surface
// shows them, in order.
func partRoots(t *testing.T, c *core, thread int64) []string {
	t.Helper()
	s, _, _ := threadSurfaceOf(t, c, thread)
	var list []map[string]any
	json.Unmarshal(s.Components, &list)
	var out []string
	for _, comp := range list {
		if comp["id"] != "root" {
			continue
		}
		for _, id := range comp["children"].([]any) {
			if name, ok := strings.CutPrefix(id.(string), threadPartRoot+"_"); ok {
				out = append(out, name)
			} else if name, ok := strings.CutPrefix(id.(string), threadExternalPrefix); ok {
				out = append(out, name) // with its mark, « extérieur »
			}
		}
	}
	return out
}

// The holder's part is a set of named blocks (#610): a block is added,
// replaced alone where it stands, placed before another, taken away;
// without a block nothing is written; ids and data keys are a block's own.
func TestPartBlocks(t *testing.T) {
	c, _ := surfaceCore(t)
	th := lightThread(t, c)
	sid := threadSurfaceID(th.ID)
	brain := surfaceCaller{author: surfaceAuthorBrain, taint: func() []string { return nil }}
	write := func(block, before string, comps []map[string]any, data map[string]any) (map[string]any, error) {
		return c.writeThreadPart(brain, th.ID, surfaceWriteIn{ID: sid, Block: block, Before: before, Components: comps, Data: data})
	}
	text := func(id, s string) []map[string]any {
		return []map[string]any{{"id": "root", "component": "Column", "children": []string{id}}, {"id": id, "component": "Text", "text": s}}
	}
	for _, b := range []string{"etat", "points", "lien"} {
		if _, err := write(b, "", text(b+"_t", "v1 "+b), nil); err != nil {
			t.Fatal(err)
		}
	}
	if got := partRoots(t, c, th.ID); !slices.Equal(got, []string{"etat", "points", "lien"}) {
		t.Fatalf("order %v", got)
	}

	// Replaced alone, where it stands.
	out, err := write("points", "", text("points_t", "v2 points"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(out["blocks"].([]string), []string{"etat", "points", "lien"}) {
		t.Errorf("result %v", out)
	}
	s, _, _ := threadSurfaceOf(t, c, th.ID)
	if !strings.Contains(string(s.Components), "v2 points") || strings.Contains(string(s.Components), "v1 points") || !strings.Contains(string(s.Components), "v1 etat") {
		t.Errorf("replace: %s", s.Components)
	}

	// before: a new block placed before one, one there moved.
	if _, err := write("resume", "etat", text("resume_t", "résumé"), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := write("lien", "etat", text("lien_t", "v2 lien"), nil); err != nil {
		t.Fatal(err)
	}
	if got := partRoots(t, c, th.ID); !slices.Equal(got, []string{"resume", "lien", "etat", "points"}) {
		t.Fatalf("before: %v", got)
	}

	// Refused, nothing written.
	for name, bad := range map[string]surfaceWriteIn{
		"no block":       {Components: oneText("x")},
		"a bad name":     {Block: "Le Bloc", Components: oneText("x")},
		"before itself":  {Block: "etat", Before: "etat", Components: oneText("x")},
		"before nothing": {Block: "neuf", Before: "nulle", Components: oneText("x")},
		"another's id":   {Block: "neuf", Components: text("etat_t", "x")},
		"another's data": {Block: "neuf", Components: boundText("/v"), Data: map[string]any{"v": 1}},
	} {
		if name == "another's data" {
			if _, err := write("chiffre", "", boundText("/v"), map[string]any{"v": 2}); err != nil {
				t.Fatal(err)
			}
		}
		bad.ID = sid
		if _, err := c.writeThreadPart(brain, th.ID, bad); err == nil {
			t.Errorf("%s: written", name)
		}
	}
	// Checked alone: a block binds none of another's data.
	if _, err := write("neuf", "", boundText("/v"), nil); err == nil {
		t.Error("a block bound another block's data")
	}
	if got := c.threadPartOf(th.ID).names(); !slices.Equal(got, []string{"resume", "lien", "etat", "points", "chiffre"}) {
		t.Fatalf("after the refusals: %v", got)
	}

	// Taken away by name; the others stay.
	if _, err := c.removeThreadPart(brain, th.ID, ""); err == nil {
		t.Error("removed with no block")
	}
	if _, err := c.removeThreadPart(brain, th.ID, "nulle"); err == nil || !strings.Contains(err.Error(), "resume, lien") {
		t.Errorf("removed a block it has not: %v", err)
	}
	if out, err := c.removeThreadPart(brain, th.ID, "lien"); err != nil || !slices.Equal(out["blocks"].([]string), []string{"resume", "etat", "points", "chiffre"}) {
		t.Fatalf("remove %v %v", out, err)
	}
	if got := partRoots(t, c, th.ID); !slices.Equal(got, []string{"resume", "etat", "points", "chiffre"}) {
		t.Fatalf("after remove: %v", got)
	}
	read, _ := callBrainTool(t, c, "read_thread", map[string]any{"thread_id": th.ID})
	if raw, _ := json.Marshal(read["blocks"]); string(raw) != `["resume","etat","points","chiffre"]` {
		t.Errorf("read_thread blocks %s", raw)
	}
}

// Two blocks may give the same source alike: it stays while one gives it.
// Given otherwise, it is refused. A part written whole before blocks is
// dropped, with the sources it gave.
func TestPartBlockSources(t *testing.T) {
	c, _ := surfaceCore(t)
	th := lightThread(t, c)
	sid := threadSurfaceID(th.ID)
	brain := surfaceCaller{author: surfaceAuthorBrain, taint: func() []string { return nil }}
	bal := map[string]surfaceSource{"/bal": {Tool: "ledger/balances"}}
	for _, b := range []string{"total", "detail"} {
		if _, err := c.writeThreadPart(brain, th.ID, surfaceWriteIn{ID: sid, Block: b, Components: []map[string]any{
			{"id": "root", "component": "Text", "text": map[string]any{"@path": "/bal/total"}}}, Sources: bal}); err != nil {
			t.Fatalf("%s: %v", b, err)
		}
	}
	c.surf.refreshing.Wait()
	if _, err := c.writeThreadPart(brain, th.ID, surfaceWriteIn{ID: sid, Block: "autre", Components: boundText("/bal/total"),
		Sources: bal, Refresh: surfaceRefresh{Every: 60}}); err == nil {
		t.Error("a source read otherwise than another block's")
	}
	if _, err := c.removeThreadPart(brain, th.ID, "total"); err != nil {
		t.Fatal(err)
	}
	if srcs := c.threadSources(th.ID); len(srcs) != 1 || srcs[0].Name != "bal" {
		t.Fatalf("the source went with one block of two: %+v", srcs)
	}
	if _, err := c.removeThreadPart(brain, th.ID, "detail"); err != nil {
		t.Fatal(err)
	}
	if srcs := c.threadSources(th.ID); len(srcs) != 0 {
		t.Fatalf("no block gives it, it stayed: %+v", srcs)
	}

	// A whole part, as written before blocks: dropped at start.
	c.putThreadSource(th.ID, threadSource{Name: "old", Tool: "ledger/balances"}, byPart)
	c.store.set(threadPartKey+"1", `{"author":"brain","components":[{"id":"thread_part","component":"Text","text":"x"}]}`)
	for _, fix := range schemaFixes {
		if err := fix(c.store.db); err != nil {
			t.Fatal(err)
		}
	}
	if c.store.get(threadPartKey+"1") != "" || len(c.threadSources(th.ID)) != 0 {
		t.Errorf("the whole part stayed: %q %+v", c.store.get(threadPartKey+"1"), c.threadSources(th.ID))
	}
	c.store.set(threadPartKey+"2", "not json")
	for _, fix := range schemaFixes {
		if err := fix(c.store.db); err != nil {
			t.Fatalf("a value that is no JSON: %v", err)
		}
	}
}

// A block whose source is not read yet is left out alone: the others
// show, and it comes once its source gives it.
func TestPartBlockLeftOutAlone(t *testing.T) {
	c, l := surfaceCore(t)
	old := surfaceReadCap
	surfaceReadCap = 100 * time.Millisecond
	t.Cleanup(func() { surfaceReadCap = old })
	hold := make(chan struct{})
	l.set(func(l *fakeLedger) { l.hold = hold })
	th := lightThread(t, c)
	sid := threadSurfaceID(th.ID)
	brain := surfaceCaller{author: surfaceAuthorBrain, taint: func() []string { return nil }}
	if _, err := c.writeThreadPart(brain, th.ID, surfaceWriteIn{ID: sid, Block: "note", Components: oneText("Une note.")}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.writeThreadPart(brain, th.ID, surfaceWriteIn{ID: sid, Block: "derniere", Components: boundText("/latest/entry"),
		Sources: map[string]surfaceSource{"/latest": {Resource: "ledger", URI: "ledger://latest"}}}); err != nil {
		t.Fatal(err)
	}
	if got := partRoots(t, c, th.ID); !slices.Equal(got, []string{"note"}) {
		t.Fatalf("while its source reads: %v", got)
	}
	close(hold)
	waitFor(t, func() bool { return slices.Equal(partRoots(t, c, th.ID), []string{"note", "derniere"}) })
}

// A block composes from code's data (#611): its steps and a source's
// items bind as the default draws them, with no step yet and once the
// thread ended alike; one bound to a source not trusted says « extérieur »
// beside it, another not.
func TestPartBindsThreadData(t *testing.T) {
	c, _ := surfaceCore(t)
	th := lightThread(t, c)
	if _, err := c.addThreadSource(th.ID, threadSource{Name: "ledger", Tool: "ledger/balances",
		Items: &sourceItems{Path: "accounts", ID: "name"}}, byBrain); err != nil {
		t.Fatal(err)
	}
	c.surf.refreshing.Wait()
	sid := threadSurfaceID(th.ID)
	brain := surfaceCaller{author: surfaceAuthorBrain, taint: func() []string { return nil }}
	rows := []map[string]any{
		{"id": "root", "component": "Column", "children": []any{"steps", "groups"}},
		{"id": "steps", "component": "Timeline", "items": map[string]any{"@path": "/thread/steps/open"}},
		{"id": "groups", "component": "List", "children": map[string]any{"path": "/thread/items/ledger/groups", "componentId": "group"}},
		{"id": "group", "component": "List", "children": map[string]any{"path": "items", "componentId": "row"}},
		{"id": "row", "component": "Item", "title": map[string]any{"@path": "title"}, "mark": map[string]any{"@path": "mark"}},
	}
	for _, in := range []surfaceWriteIn{
		{ID: sid, Block: "comptes", Components: rows},
		{ID: sid, Block: "mot", Components: oneText("Rien d'autre.")},
	} {
		if _, err := c.writeThreadPart(brain, th.ID, in); err != nil {
			t.Fatalf("%s: %v", in.Block, err)
		}
	}
	s, _, _ := threadSurfaceOf(t, c, th.ID)
	var list []map[string]any
	json.Unmarshal(s.Components, &list)
	ids := map[string]bool{}
	for _, comp := range list {
		ids[comp["id"].(string)] = true
	}
	if !ids[threadExternalPrefix+"comptes"] || !ids[threadMarkPrefix+"comptes"] || ids[threadExternalPrefix+"mot"] || !ids[partRoot("mot")] {
		t.Errorf("« extérieur » beside the wrong blocks: %v", ids)
	}
	if ids["thread_items_0_groups"] {
		t.Error("the default stayed")
	}

	// Ended: no longer read, the same keys empty, the block still there.
	c.setThread(th.ID, map[string]any{"state": threadDone})
	_, data, _ := threadSurfaceOf(t, c, th.ID)
	if got := partRoots(t, c, th.ID); !slices.Equal(got, []string{"comptes", "mot"}) {
		t.Errorf("ended: blocks %v", got)
	}
	if _, srcs := itemSources(data); srcs["ledger"]["groups"] == nil {
		t.Errorf("ended: %v", data["items"])
	}
}

// A block is « extérieur » by the source it binds, by whole names.
func TestPartBlockExternal(t *testing.T) {
	untrusted := map[string]bool{"mail": true}
	for _, tc := range []struct {
		path string
		want bool
	}{{"/mail/0/subject", true}, {"/thread/items/mail/groups", true}, {"/mails/0", false}, {"/thread/items/mails/groups", false}, {"/thread/steps/open", false}} {
		comps, _ := json.Marshal([]map[string]any{{"id": "root", "component": "Text", "text": map[string]any{"@path": tc.path}}})
		if got := (partBlock{Components: comps}).external(untrusted); got != tc.want {
			t.Errorf("%s: %v", tc.path, got)
		}
	}
}
