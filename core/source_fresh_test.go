package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// Live sources kept fresh after a change (source_fresh.go).

// quickFresh makes a re-read wait moments, not a second.
func quickFresh(t *testing.T) {
	t.Helper()
	q, m := freshQuiet, freshMaxWait
	freshQuiet, freshMaxWait = 250*time.Millisecond, 2*time.Second
	t.Cleanup(func() { freshQuiet, freshMaxWait = q, m })
}

// freshView is a core whose node lists the monitor's received mails m1,
// m2, m3 (a resource of the monitor server, whose move takes ids), read
// once.
func freshView(t *testing.T) (*core, *fakeMonitor, string) {
	t.Helper()
	quickFresh(t)
	tw, _, fm, id := actionsWeb(t, archiveAction)
	return tw.c, fm, id
}

// brainCall is a call the brain makes on the monitor server, the user's
// permission given (its ticket).
func brainCall(t *testing.T, c *core, tool string, input map[string]any) {
	t.Helper()
	c.store.addTicket("t-brain", claudeToolName("monitor", tool), input)
	raw, _ := json.Marshal(input)
	if res := c.proxyCall(context.Background(), "t-brain", []string{"monitor"}, c.pool.get("monitor"), tool, raw); res.IsError {
		t.Fatalf("%s: %s", tool, resultText(res))
	}
}

// states are the items of a node's source as the page gets them, their
// _state by id.
func states(t *testing.T, c *core, id, block, source string) map[string]string {
	t.Helper()
	d, err := c.nodes.Read(id, 0)
	if err != nil {
		t.Fatal(err)
	}
	v := c.blockView(id, d.Node.Block(block), c.nodeReads(id))
	src, _ := v.Data[source].(map[string]any)
	list, _ := src["messages"].([]any)
	out := map[string]string{}
	for _, it := range list {
		o, _ := it.(map[string]any)
		s, _ := o[itemStateField].(string)
		out[o["id"].(string)] = s
	}
	return out
}

// sourceSaid tells whether the stream was told a node's source moved
// after seq.
func sourceSaid(c *core, id string, after int64) bool {
	c.graph.mu.Lock()
	defer c.graph.mu.Unlock()
	return slices.ContainsFunc(c.graph.events, func(e graphEvent) bool { return e.Seq > after && e.Kind == graphEvSource && e.Node == id })
}

func graphSeq(c *core) int64 {
	c.graph.mu.Lock()
	defer c.graph.mu.Unlock()
	return c.graph.seq
}

func setGot(fm *fakeMonitor, ids ...string) {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	var got []map[string]any
	for _, id := range ids {
		got = append(got, map[string]any{"id": id, "subject": "Mail " + id, "from": "Sam"})
	}
	fm.got = got
}

// The brain trashes two mails from the conversation: they show done at
// once, the open pages are told, and the source is read again moments
// after: they are gone. One the server still lists comes back.
func TestWriteCallRereadsItsServer(t *testing.T) {
	c, fm, id := freshView(t)
	setGot(fm, "m2") // what the server holds once the call ran
	seq := graphSeq(c)
	brainCall(t, c, "move", map[string]any{"ids": []string{"m1", "m3"}, "to": "trash"})
	if got := states(t, c, id, "inbox", "received"); got["m1"] != itemDone || got["m3"] != itemDone || got["m2"] != "" {
		t.Fatalf("at once: %v", got)
	}
	if !sourceSaid(c, id, seq) {
		t.Error("the open pages were not told at once")
	}
	seq = graphSeq(c)
	c.live.wg.Wait() // the re-read, a moment after
	if got := states(t, c, id, "inbox", "received"); len(got) != 1 || got["m2"] != "" {
		t.Fatalf("read again: %v", got)
	}
	if !sourceSaid(c, id, seq) {
		t.Error("the new value was not pushed")
	}

	// Named, but the server still lists it: back, as it is.
	brainCall(t, c, "move", map[string]any{"ids": []string{"m2"}, "to": "archive"})
	if got := states(t, c, id, "inbox", "received"); got["m2"] != itemDone {
		t.Fatalf("at once: %v", got)
	}
	seq = graphSeq(c)
	c.live.wg.Wait()
	if got := states(t, c, id, "inbox", "received"); got["m2"] != "" {
		t.Fatalf("the item did not come back: %v", got)
	}
	if !sourceSaid(c, id, seq) {
		t.Error("its return was not pushed, though the value did not change")
	}
}

// A read changes nothing: a tool its server marks readOnlyHint reads
// nothing again and shows nothing done.
func TestReadCallRereadsNothing(t *testing.T) {
	c, fm, id := freshView(t)
	fm.mu.Lock()
	before := fm.calls
	fm.mu.Unlock()
	brainCall(t, c, "message", map[string]any{"id": "m1"})
	c.fresh.mu.Lock()
	waiting := c.fresh.timer != nil || len(c.fresh.asks) > 0 || len(c.fresh.leaving) > 0
	c.fresh.mu.Unlock()
	if waiting {
		t.Fatal("a read asked for a re-read")
	}
	c.live.wg.Wait()
	fm.mu.Lock()
	after := fm.calls
	fm.mu.Unlock()
	if after != before {
		t.Errorf("the source was read %d times", after-before)
	}
	if got := states(t, c, id, "inbox", "received"); got["m1"] != "" {
		t.Errorf("m1 shows %q", got["m1"])
	}
}

// The calls of a moment are one read: three calls, the source read
// once, after the last.
func TestRereadsGathered(t *testing.T) {
	c, fm, _ := freshView(t)
	freshQuiet, freshMaxWait = 500*time.Millisecond, 5*time.Second // calls closer than that, even on a slow runner
	fm.mu.Lock()
	before := fm.calls
	fm.mu.Unlock()
	for _, m := range []string{"m1", "m2", "m3"} {
		brainCall(t, c, "move", map[string]any{"ids": []string{m}, "to": "archive"})
	}
	c.live.wg.Wait()
	fm.mu.Lock()
	after := fm.calls
	fm.mu.Unlock()
	if after-before != 1 {
		t.Errorf("read %d times for three calls", after-before)
	}
}

// What a call changes is read again where it may show: the sources of its
// server only, of the account it names and those that name none; all of
// them when it names none.
func TestRereadScope(t *testing.T) {
	c, _, _ := viewCore(t)
	src := func(name, account string) string {
		s := map[string]any{"tool": "monitor/status", "args": map[string]any{"service": "web"}}
		if account != "" {
			s["args"].(map[string]any)["account"] = account
		}
		raw, _ := json.Marshal(map[string]any{"components": []map[string]any{{"id": "root", "component": "Text", "text": "x"}}, "sources": map[string]any{name: s}})
		return "```eriphe:block " + name + "\n" + string(raw) + "\n```\n"
	}
	perso := mustNode(t, c, nodestore.New{Title: "Perso", Lifetime: nodefile.Standing, Body: "x\n\n" + src("p", "perso")})
	travail := mustNode(t, c, nodestore.New{Title: "Travail", Lifetime: nodefile.Standing, Body: "x\n\n" + src("w", "travail")})
	all := mustNode(t, c, nodestore.New{Title: "Tout", Lifetime: nodefile.Standing, Body: "x\n\n" + src("a", "")})
	other := mustNode(t, c, nodestore.New{Title: "Ailleurs", Lifetime: nodefile.Standing,
		Body: "x\n\n```eriphe:block o\n" + `{"components": [{"id": "root", "component": "Text", "text": "x"}], "sources": {"o": {"tool": "agenda/today"}}}` + "\n```\n"})
	for _, tc := range []struct {
		accounts map[string]bool
		want     []string
	}{
		{map[string]bool{"perso": true}, []string{perso, all}},
		{map[string]bool{"": true}, []string{perso, travail, all}},
		{map[string]bool{"perso": true, "travail": true}, []string{perso, travail, all}},
	} {
		got := c.sourcesReading("monitor", tc.accounts)
		var ids []string
		for id := range got {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		want := slices.Clone(tc.want)
		slices.Sort(want)
		if !slices.Equal(ids, want) {
			t.Errorf("%v: read %v, want %v", tc.accounts, got, tc.want)
		}
		if _, ok := got[other]; ok {
			t.Errorf("%v: another server's source is read", tc.accounts)
		}
	}
}

// A card's yes and a view's action run through catalogCall: the same.
func TestApprovedCallRereads(t *testing.T) {
	c, fm, id := freshView(t)
	setGot(fm, "m1", "m3")
	raw, _ := json.Marshal(map[string]any{"ids": []string{"m2"}, "to": "trash"})
	if _, _, code, why := c.catalogCall(context.Background(), claudeToolName("monitor", "move"), string(raw), false, func(map[string]any) {}); code != "" {
		t.Fatalf("call: %s %s", code, why)
	}
	if got := states(t, c, id, "inbox", "received"); got["m2"] != itemDone {
		t.Fatalf("at once: %v", got)
	}
	c.live.wg.Wait()
	if got := states(t, c, id, "inbox", "received"); len(got) != 2 || got["m1"] != "" || got["m3"] != "" {
		t.Fatalf("read again: %v", got)
	}
}

// A read already under way when the call ran says nothing of what the
// call named: only one begun after it does.
func TestReadBeforeCallKeepsLeaving(t *testing.T) {
	c, _, id := freshView(t)
	began := time.Now()
	brainCall(t, c, "move", map[string]any{"ids": []string{"m1"}, "to": "trash"})
	if c.sourceReadSince(id, "received", began) {
		t.Error("a read begun before the call cleared what it named")
	}
	if !c.leavingItems(id, "received")["m1"] {
		t.Error("m1 no longer leaving")
	}
	if !c.sourceReadSince(id, "received", time.Now()) || c.leavingItems(id, "received")["m1"] {
		t.Error("a read begun after the call kept m1 leaving")
	}
	c.live.wg.Wait()
}

// The conversation's call on a view's items wakes its skill, once; a
// task's own call (a skill's run) never does.
func TestConversationCallWakesTheView(t *testing.T) {
	quickFresh(t)
	c, _, node, r := mailViewOf(t)
	woken := func() int {
		var n int
		c.store.db.QueryRow(`SELECT COUNT(*) FROM event_journal WHERE kind = ? AND key = ?`, evNodeGesture, nodeRef(node)).Scan(&n)
		return n
	}
	// The run's own call: read again, not woken.
	in := map[string]any{"ids": []string{"m2"}, "to": "archive"}
	c.store.addTicket(r.Task, claudeToolName("monitor", "move"), in)
	raw, _ := json.Marshal(in)
	if res := c.proxyCall(context.Background(), r.Task, []string{"monitor"}, c.pool.get("monitor"), "move", raw); res.IsError {
		t.Fatalf("the run's call: %s", resultText(res))
	}
	c.live.wg.Wait()
	if n := woken(); n != 0 {
		t.Fatalf("the run woke its own skill %d times", n)
	}
	// The brain's: woken.
	brainCall(t, c, "move", map[string]any{"ids": []string{"m1"}, "to": "archive"})
	c.live.wg.Wait()
	if n := woken(); n != 1 {
		t.Errorf("woken %d times", n)
	}
	// A call that names none of its items wakes nothing.
	brainCall(t, c, "move", map[string]any{"ids": []string{"zz"}, "to": "archive"})
	c.live.wg.Wait()
	if n := woken(); n != 1 {
		t.Errorf("woken by a call on nothing it lists: %d", n)
	}
	// A call of the conversation said yes on its card (on no item): woken.
	raw, _ = json.Marshal(map[string]any{"ids": []string{"m2"}, "to": "archive"})
	if _, _, code := c.approvedCall(context.Background(), accessQuestion{TaskID: "t-brain", Input: string(raw)}, claudeToolName("monitor", "move")); code != "" {
		t.Fatalf("approved call: %s", code)
	}
	c.live.wg.Wait()
	if n := woken(); n != 2 {
		t.Errorf("the card's yes woke it %d times in all", n)
	}
}

// A page opened reads again its sources older than a minute, once a
// minute at most; a fresh one is not read.
func TestPageOpenReadsStale(t *testing.T) {
	c, fm, id := freshView(t)
	w := &web{c: c}
	open := func() {
		r := httptest.NewRequest(http.MethodGet, "/api/nodes/"+id, nil)
		r.SetPathValue("id", id)
		rw := httptest.NewRecorder()
		w.handleNodePage(rw, r, nil)
		if rw.Code != http.StatusOK {
			t.Fatalf("page: %d", rw.Code)
		}
		c.live.wg.Wait()
	}
	calls := func() int {
		fm.mu.Lock()
		defer fm.mu.Unlock()
		return fm.calls
	}
	before := calls()
	open()
	if calls() != before {
		t.Fatal("a fresh source was read again")
	}
	c.store.db.Exec(`UPDATE node_reads SET read = ? WHERE node = ?`, time.Now().Add(-2*time.Minute).Unix(), id)
	setGot(fm, "m1")
	open()
	if calls() != before+1 {
		t.Fatalf("a stale source read %d times", calls()-before)
	}
	if got := states(t, c, id, "inbox", "received"); len(got) != 1 {
		t.Errorf("after the read: %v", got)
	}
	c.store.db.Exec(`UPDATE node_reads SET read = ? WHERE node = ?`, time.Now().Add(-2*time.Minute).Unix(), id)
	open()
	if calls() != before+1 {
		t.Error("read again within the minute")
	}
}

// A server whose tools a node reads has all its resources followed: what
// it says changed reaches its views.
func TestToolServerResourcesFollowed(t *testing.T) {
	c, _, _ := viewCore(t)
	u := c.pool.get("monitor")
	c.followNodeResources()
	u.mu.Lock()
	none := len(u.followed)
	u.mu.Unlock()
	if none != 0 {
		t.Fatalf("followed with no node: %d", none)
	}
	mustNode(t, c, nodestore.New{Title: "État", Lifetime: nodefile.Standing,
		Body: "x\n\n```eriphe:block s\n" + `{"components": [{"id": "root", "component": "Text", "text": "x"}], "sources": {"s": {"tool": "monitor/status", "args": {"service": "web"}}}}` + "\n```\n"})
	c.followNodeResources()
	u.mu.Lock()
	followed := slices.Clone(u.followed)
	u.mu.Unlock()
	if !slices.Equal(followed, []string{"monitor://invitations", "monitor://received"}) {
		t.Errorf("followed %v", followed)
	}
}

// A server that says a resource changed has its sources read again,
// whatever resource it names.
func TestResourceUpdateRereadsItsServer(t *testing.T) {
	c, fm, id := freshView(t)
	setGot(fm, "m3")
	said := freshSaid
	freshSaid = 600 * time.Millisecond
	t.Cleanup(func() { freshSaid = said })
	c.nodeResourceUpdated("monitor", "monitor://invitations")
	c.live.wg.Wait()
	if got := states(t, c, id, "inbox", "received"); len(got) != 1 || got["m3"] != "" {
		t.Fatalf("read again: %v", got)
	}
	// Said again at once, twice: one read, at the end of its floor.
	setGot(fm, "m1", "m3")
	c.nodeResourceUpdated("monitor", "monitor://received")
	c.nodeResourceUpdated("monitor", "monitor://received")
	c.live.wg.Wait()
	if got := states(t, c, id, "inbox", "received"); len(got) != 1 {
		t.Fatalf("read before its floor: %v", got)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(states(t, c, id, "inbox", "received")) != 2 {
		if time.Now().After(deadline) {
			t.Fatal("never read after its floor")
		}
		time.Sleep(20 * time.Millisecond)
	}
	c.live.wg.Wait()
}
