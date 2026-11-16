package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// sourcesOf is what a thread's surface shows of its sources: each one's
// caption and its items' ids.
func sourcesOf(t *testing.T, c *core, id int64) ([]string, [][]string) {
	t.Helper()
	_, data, _ := threadSurfaceOf(t, c, id)
	var captions []string
	var items [][]string
	list, _ := data["sources"].([]any)
	for _, x := range list {
		m, _ := x.(map[string]any)
		caption, _ := m["caption"].(string)
		captions = append(captions, caption)
		var ids []string
		rows := sourceRows(m)
		for _, r := range rows {
			row, _ := r.(map[string]any)
			id, _ := row["id"].(string)
			ids = append(ids, id)
		}
		items = append(items, ids)
	}
	return captions, items
}

// A thread carries a source that lists things: code reads it, without a
// model, and its surface shows each item; a read again changes the data,
// not the components; a read refused keeps what was read last; its value
// is where the holder's part binds it; a source of a server not trusted
// says « extérieur » and taints the surface, whose cards still answer.
func TestThreadSourceItems(t *testing.T) {
	c, l := surfaceCore(t)
	th := lightThread(t, c)
	msgs := c.store.lastMessageSeq()
	src := threadSource{Name: "ledger", Tool: "ledger/balances", Items: &sourceItems{Path: "accounts", ID: "name"}}
	if _, err := c.addThreadSource(th.ID, src, byBrain); err != nil {
		t.Fatal(err)
	}
	c.surf.refreshing.Wait()
	captions, items := sourcesOf(t, c, th.ID)
	if len(items) != 1 || strings.Join(items[0], ",") != "Courant" {
		t.Fatalf("items %v", items)
	}
	if !strings.Contains(captions[0], "Source ledger") || !strings.Contains(captions[0], "sans modèle") || !strings.Contains(captions[0], "extérieur") {
		t.Errorf("caption %q", captions[0])
	}
	s, _, meta := threadSurfaceOf(t, c, th.ID)
	var data map[string]any
	json.Unmarshal(s.Data, &data)
	if total, _ := data["ledger"].(map[string]any)["total"].(float64); total != 120 {
		t.Errorf("its value is not at /ledger: %v", data["ledger"])
	}
	if !meta.Tainted || !meta.Answers || !strings.Contains(s.Tainted, "source:ledger") {
		t.Errorf("meta %+v tainted %q", meta, s.Tainted)
	}
	if c.store.lastMessageSeq() != msgs {
		t.Error("a model was told")
	}

	// Read again: the data changes, not the components.
	l.set(func(l *fakeLedger) { l.accounts = append(l.accounts, ledgerAccount{Name: "Épargne", Balance: 30}) })
	before := string(s.Components)
	c.readThreadSources(context.Background(), th.ID, nil, time.Now())
	s, _, _ = threadSurfaceOf(t, c, th.ID)
	if _, items = sourcesOf(t, c, th.ID); strings.Join(items[0], ",") != "Courant,Épargne" {
		t.Fatalf("read again: %v", items)
	}
	if string(s.Components) != before {
		t.Errorf("the components changed:\n%s\n%s", before, s.Components)
	}

	// Its time comes at the engine's tick.
	calls := l.count()
	c.threadSourcesDue(time.Now().Add(time.Duration(surfaceEveryDef) * time.Minute))
	c.surf.refreshing.Wait()
	if l.count() != calls+1 {
		t.Errorf("not read at its time: %d calls", l.count()-calls)
	}

	// A read refused (the user sets the tool to ask first: no free read)
	// keeps the last, and calls nothing.
	u := c.pool.get("ledger")
	u.mu.Lock()
	u.srv.Access = accessAsk
	u.mu.Unlock()
	calls = l.count()
	c.readThreadSources(context.Background(), th.ID, nil, time.Now())
	captions, items = sourcesOf(t, c, th.ID)
	if l.count() != calls || strings.Join(items[0], ",") != "Courant,Épargne" || !strings.Contains(captions[0], "en attente") {
		t.Errorf("refused: %d calls, %v %q", l.count()-calls, items, captions)
	}
	u.mu.Lock()
	u.srv.Access = accessReads
	u.mu.Unlock()

	// Taken away by its name alone.
	if out, isErr := callBrainTool(t, c, "link_thread", map[string]any{"thread_id": th.ID, "source": map[string]any{"name": "ledger"}}); isErr {
		t.Fatalf("drop: %v", out)
	}
	if captions, _ = sourcesOf(t, c, th.ID); len(captions) != 0 {
		t.Errorf("still shown: %v", captions)
	}
	s, _, _ = threadSurfaceOf(t, c, th.ID)
	data = nil
	json.Unmarshal(s.Data, &data)
	if _, ok := data["ledger"]; ok || s.Tainted != "" {
		t.Errorf("its value stayed: %s %q", s.Data, s.Tainted)
	}
}

// A source is checked when added: its shape, its name, and that code may
// read it (sourceWhy); a monitor is linked once approved.
func TestThreadSourceChecks(t *testing.T) {
	c, _ := surfaceCore(t)
	th := lightThread(t, c)
	for name, src := range map[string]threadSource{
		"no kind":       {Name: "x"},
		"two kinds":     {Name: "x", Tool: "ledger/balances", Resource: "ledger", URI: "ledger://latest"},
		"code's name":   {Name: "thread", Tool: "ledger/balances"},
		"bad name":      {Name: "Mes mails", Tool: "ledger/balances"},
		"not readable":  {Name: "x", Tool: "ledger/move_funds"},
		"not a server":  {Name: "x", Tool: "nope/list"},
		"items no id":   {Name: "x", Tool: "ledger/balances", Items: &sourceItems{Path: "accounts"}},
		"items bad":     {Name: "x", Tool: "ledger/balances", Items: &sourceItems{ID: "a[b"}},
		"every":         {Name: "x", Tool: "ledger/balances", Every: 5000},
		"no monitor":    {Monitor: "nope"},
		"monitor items": {Monitor: "cpu", Items: &sourceItems{ID: "id"}},
	} {
		if _, err := c.addThreadSource(th.ID, src, byBrain); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	c.closeLight(th.ID, "fait", byBrain)
	if _, err := c.addThreadSource(th.ID, threadSource{Name: "x", Tool: "ledger/balances"}, byBrain); err == nil {
		t.Error("added to an ended thread")
	}
}

// A resource source is read again when its server says it changed, and
// its server is asked for those updates.
func TestThreadSourceResource(t *testing.T) {
	c, l := surfaceCore(t)
	th := lightThread(t, c)
	if _, err := c.addThreadSource(th.ID, threadSource{Name: "latest", Resource: "ledger", URI: "ledger://latest"}, byBrain); err != nil {
		t.Fatal(err)
	}
	c.surf.refreshing.Wait()
	c.followSurfaceResources()
	waitFor(t, func() bool { return l.listens("ledger://latest") })
	l.set(func(l *fakeLedger) { l.latest = "second" })
	c.surfaceResourceUpdated("ledger", "ledger://latest")
	c.surf.refreshing.Wait()
	s, _, _ := threadSurfaceOf(t, c, th.ID)
	if !strings.Contains(string(s.Data), `"entry":"second"`) {
		t.Errorf("not read again: %s", s.Data)
	}
}

// The holder's part may bring sources (#571 lifted the refusal): they
// become the thread's, read by code, their value where the part binds
// it; written again without them, or removed, they go.
func TestThreadPartSources(t *testing.T) {
	c, _ := surfaceCore(t)
	th := lightThread(t, c)
	sid := threadSurfaceID(th.ID)
	brain := surfaceCaller{author: surfaceAuthorBrain, taint: func() []string { return nil }}
	part := func() surfaceWriteIn {
		return surfaceWriteIn{ID: sid, Components: boundText("/bal/total"),
			Sources: map[string]surfaceSource{"/bal": {Tool: "ledger/balances"}}}
	}
	if _, err := c.writeThreadPart(brain, th.ID, part()); err != nil {
		t.Fatal(err)
	}
	c.surf.refreshing.Wait()
	srcs := c.threadSources(th.ID)
	if len(srcs) != 1 || srcs[0].Name != "bal" || srcs[0].By != byPart || srcs[0].Value == nil {
		t.Fatalf("sources %+v", srcs)
	}
	s, _, _ := threadSurfaceOf(t, c, th.ID)
	if !strings.Contains(string(s.Data), `"bal":{`) {
		t.Errorf("no value at /bal: %s", s.Data)
	}
	for name, bad := range map[string]surfaceWriteIn{
		"nested":      {ID: sid, Components: boundText("/a/b"), Sources: map[string]surfaceSource{"/a/b": {Tool: "ledger/balances"}}},
		"code's data": {ID: sid, Components: boundText("/thread"), Sources: map[string]surfaceSource{"/thread": {Tool: "ledger/balances"}}},
		"not a read":  {ID: sid, Components: boundText("/m"), Sources: map[string]surfaceSource{"/m": {Tool: "ledger/move_funds"}}},
		"unbound":     {ID: sid, Components: boundText("/bal/nope"), Sources: map[string]surfaceSource{"/bal": {Tool: "ledger/balances"}}},
	} {
		if _, err := c.writeThreadPart(brain, th.ID, bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := c.writeThreadPart(brain, th.ID, surfaceWriteIn{ID: sid, Components: oneText("x")}); err != nil {
		t.Fatal(err)
	}
	if srcs := c.threadSources(th.ID); len(srcs) != 0 {
		t.Errorf("the part's sources stayed: %+v", srcs)
	}
	if _, err := c.writeThreadPart(brain, th.ID, part()); err != nil {
		t.Fatal(err)
	}
	c.surf.refreshing.Wait()
	if _, err := c.removeThreadPart(brain, th.ID); err != nil {
		t.Fatal(err)
	}
	if srcs := c.threadSources(th.ID); len(srcs) != 0 {
		t.Errorf("removed, its sources stayed: %+v", srcs)
	}

	// Written with its source, the part shows at once, and still once the
	// thread ended; a source the thread has already is bound, not given
	// again.
	if _, err := c.writeThreadPart(brain, th.ID, part()); err != nil {
		t.Fatal(err)
	}
	shown := func(when string) {
		t.Helper()
		s, _, _ := threadSurfaceOf(t, c, th.ID)
		if !strings.Contains(string(s.Components), `"thread_part"`) || s.Look.LastError != nil {
			t.Fatalf("%s: the part is left out: %s %+v", when, s.Components, s.Look.LastError)
		}
	}
	shown("written")
	if _, err := c.addThreadSource(th.ID, threadSource{Name: "accounts", Tool: "ledger/balances"}, byBrain); err != nil {
		t.Fatal(err)
	}
	c.surf.refreshing.Wait()
	again := surfaceWriteIn{ID: sid, Components: boundText("/accounts/total"),
		Sources: map[string]surfaceSource{"/accounts": {Tool: "ledger/balances"}}}
	if _, err := c.writeThreadPart(brain, th.ID, again); err == nil {
		t.Error("a part took the name of the thread's source")
	}
	if srcs := c.threadSources(th.ID); len(srcs) != 2 || srcs[0].Name != "accounts" || srcs[0].By != byBrain || srcs[1].By != byPart {
		t.Errorf("sources %+v", srcs)
	}
	if err := c.closeLight(th.ID, "fait", byBrain); err != nil {
		t.Fatal(err)
	}
	shown("ended")
}
