package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// Views on nodes (#631), played on faked servers: the monitor server
// stands for the mail and calendar servers (its resources list messages
// and invitations, send and respond act on one of them).

// viewCore is pollCore with a graph, a device and the skills listening.
func viewCore(t *testing.T) (*core, *fakeMonitor, *time.Time) {
	t.Helper()
	c, fm, now := pollCore(t)
	dir := t.TempDir()
	s, _, err := nodestore.Open(filepath.Join(dir, "nodes"), filepath.Join(dir, "nodes.db"), nodestore.Options{Refuse: refuseSecret})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	c.nodes = s
	c.store.registerDevice("pc", hashSecret("pc-secret"))
	c.store.setDeviceStatus("pc", true, []byte(`{"runtimes":["claude-code"]}`))
	c.subscribeEvents(c.queueSkillEvent)
	waitResource(t, c, "monitor://received")
	waitResource(t, c, "monitor://invitations")
	return c, fm, now
}

func waitResource(t *testing.T, c *core, uri string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !slices.Contains(c.pool.get("monitor").resourceURIs(), uri) {
		if time.Now().After(deadline) {
			t.Fatalf("%s is not listed", uri)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// permission is a task's request to call a tool, as its events say it.
func permission(pid, tool string, input map[string]any) EventRow {
	return EventRow{Seq: 1, Time: time.Now(), Type: "permission", Data: map[string]any{"id": pid, "tool_name": tool, "input": input}}
}

// listBlock is a block that lists a source's items.
func listBlock(name, source string, src map[string]any) string {
	raw, _ := json.Marshal(map[string]any{
		"components": []map[string]any{
			{"id": "root", "component": "List", "children": map[string]any{"path": "/" + source + "/" + src["items"].(map[string]any)["path"].(string), "componentId": "row"}},
			{"id": "row", "component": "Text", "text": map[string]any{"@path": "title"}},
		},
		"sources": map[string]any{source: src},
	})
	return "```eriphe:block " + name + "\n" + string(raw) + "\n```\n"
}

// block reads a node's block as its file holds it.
func block(t *testing.T, c *core, id, name string) *nodefile.Block {
	t.Helper()
	d, err := c.nodes.Read(id, 0)
	if err != nil {
		t.Fatal(err)
	}
	b := d.Node.Block(name)
	if b == nil {
		t.Fatalf("node %s has no block %s", id, name)
	}
	return b
}

// sentNow is what the faked server was asked to do.
func sentNow(fm *fakeMonitor) []map[string]any {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	return slices.Clone(fm.sent)
}

func waitSent(t *testing.T, fm *fakeMonitor, n int) []map[string]any {
	t.Helper()
	for i := 0; i < 300; i++ {
		if got := sentNow(fm); len(got) >= n {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("sent %v", sentNow(fm))
	return nil
}

// cardsOf are the pending proposals of a kind.
func cardsOf(c *core, kind string) []proposal {
	var out []proposal
	for _, p := range c.pendingProposals() {
		if p.Kind == kind {
			out = append(out, p)
		}
	}
	return out
}

// « Surveille mes mails »: one card shows the standing node, its monitor
// and its skill; nothing exists before the yes, and the yes makes all
// three. A mail comes: the monitor wakes the skill, whose run works in the
// standing node: it notes the mail, opens « Répondre à Sam » with a draft,
// and its send is kept for a « Send? » card on the mail. Nothing is sent
// before that yes.
func TestMailViewOneYes(t *testing.T) {
	c, fm, now := viewCore(t)
	mails := map[string]any{"resource": "monitor", "uri": "monitor://received", "items": map[string]any{"path": "messages", "id": "id", "title": "subject", "detail": "from"}}
	out, isErr := callBrainTool(t, c, "propose_view", map[string]any{
		"title": "Mes mails", "goal": "Sam voit ses mails et ce qui attend une réponse.",
		"body":    "Les mails reçus.\n\n" + listBlock("recus", "mails", mails),
		"monitor": map[string]any{"name": "mails", "server": "monitor", "resource": "monitor://received", "cond": "new", "path": "messages", "id": "id", "every": "1m"},
		"skill": map[string]any{"name": "tri-mails", "description": "Trie les mails reçus.", "when": "monitor.new key=mails", "tools": []string{"monitor"},
			"instructions": "Note chaque mail. Pour un mail qui attend une réponse, ouvre un node « Répondre à … » avec un brouillon, et appelle l'envoi : il attend le oui."},
	})
	if isErr {
		t.Fatalf("propose_view: %v", out)
	}
	node, _ := out["node"].(string)
	if !nodefile.IDOK(node) {
		t.Fatalf("out %v", out)
	}
	if _, err := c.nodes.Get(node); err == nil {
		t.Fatal("the node exists before the yes")
	}
	if _, err := c.monitorNamed("mails"); err == nil {
		t.Fatal("the monitor exists before the yes")
	}
	if k, _ := c.store.skill("tri-mails"); k != nil {
		t.Fatal("the skill exists before the yes")
	}
	views := cardsOf(c, cardViewKind)
	if len(views) != 1 || len(cardsOf(c, cardMonitor)) != 0 || len(cardsOf(c, cardSkill)) != 0 {
		t.Fatalf("cards %+v", c.pendingProposals())
	}
	v := views[0]
	fields, _ := json.Marshal(v.FieldsIn("fr"))
	for _, want := range []string{"recus (monitor monitor://received)", "monitor monitor://received", "monitor.new", "tri-mails"} {
		if !strings.Contains(string(fields), want) {
			t.Errorf("the card lacks %q: %s", want, fields)
		}
	}
	if !strings.Contains(v.Text("fr"), "Mes mails") {
		t.Errorf("text %q", v.Text("fr"))
	}

	// One yes: the three.
	if err := c.decideProposal(context.Background(), cardViewKind, v.ID, true); err != nil {
		t.Fatal(err)
	}
	c.live.wg.Wait()
	sum, err := c.nodes.Get(node)
	if err != nil || sum.Lifetime != nodefile.Standing || sum.Title != "Mes mails" {
		t.Fatalf("node %+v %v", sum, err)
	}
	if m, err := c.monitorNamed("mails"); err != nil || m.State != monitorOn {
		t.Fatalf("monitor %+v %v", m, err)
	}
	k, _ := c.store.skill("tri-mails")
	if k == nil || !k.Active() {
		t.Fatalf("skill %+v", k)
	}
	if sk, _ := c.store.skillVersion(k.Approved); sk.Manifest.Trigger.Node != node {
		t.Fatalf("the skill works in %q", sk.Manifest.Trigger.Node)
	}
	if p := c.proposalOf(cardViewKind, v.ID); p.Pending || p.Outcome != cardApproved {
		t.Fatalf("the card %+v", p)
	}
	// The monitor server is not trusted: the block that reads it is external.
	if !block(t, c, node, "recus").External {
		t.Error("a block reading a server not trusted is not external")
	}

	// A mail from Sam.
	lookAt(c, *now) // the baseline
	fm.mu.Lock()
	fm.got = []map[string]any{{"id": "m1", "subject": "Rendez-vous jeudi ?", "from": "sam@example.org"}}
	fm.mu.Unlock()
	lookAt(c, now.Add(time.Minute))
	c.tickSkills(context.Background(), now.Add(time.Minute))
	rs := runsOfSkill(t, c, "tri-mails")
	if len(rs) != 1 || rs[0].Task == "" {
		t.Fatalf("runs %+v", rs)
	}
	r := rs[0]
	if got := c.taskNode(r.Task); got != node {
		t.Fatalf("the run works in %q, not the view's %s", got, node)
	}
	task, _ := c.store.task(r.Task)
	if !strings.Contains(task.Prompt, "« Mes mails »") {
		t.Errorf("the run's prompt does not name its node: %s", task.Prompt)
	}
	c.readNode(context.Background(), node, []string{"mails"}, time.Now())

	// The run: it notes the mail, opens the reply with its draft.
	mustNodes(t, c, r.Task, "item_note", map[string]any{"source": "mails", "item": "m1", "note": "Sam propose jeudi.", "state": "act"})
	kid := mustNodes(t, c, r.Task, "node_open", map[string]any{"title": "Répondre à Sam", "goal": "Répondre à Sam pour jeudi.", "body": "Brouillon : jeudi 10 h me va."})
	if items, _ := c.nodes.Items(node); len(items) != 1 || items[0].Item != "m1" || items[0].State != "act" {
		t.Fatalf("items %+v", items)
	}
	kids, _ := c.nodes.Children(node)
	if len(kids) != 1 || kids[0].ID != kid["id"] || kids[0].Title != "Répondre à Sam" {
		t.Fatalf("children %+v", kids)
	}
	// Its send: kept.
	ev := []EventRow{permission("s1", "mcp__monitor__send", map[string]any{"id": "m1", "body": "Jeudi 10 h me va."})}
	c.store.addEvents(r.Task, ev)
	c.applyPolicy(r.Task, ev)
	endTurn(t, c, r.Task, "Un mail de Sam à répondre.")
	if got := sentNow(fm); len(got) != 0 {
		t.Fatalf("sent before the yes: %v", got)
	}
	cards := cardsOf(c, cardCalls)
	if len(cards) != 1 {
		t.Fatalf("calls cards %+v", c.pendingProposals())
	}
	asks := c.batchAsks(cards[0].ID)
	if len(asks) != 1 || asks[0].Node != node || asks[0].Source != "mails" || asks[0].Item != "m1" {
		t.Fatalf("asks %+v", asks)
	}
	if o := cards[0].Origin("fr"); o.Node != node {
		t.Errorf("origin %+v", o)
	}
	if err := c.decideProposal(context.Background(), cardCalls, cards[0].ID, true); err != nil {
		t.Fatal(err)
	}
	if got := waitSent(t, fm, 1); got[0]["tool"] != "send" || got[0]["id"] != "m1" {
		t.Fatalf("sent %v", got)
	}
	// The standing node is still the view's: never done by a run.
	if sum, _ := c.nodes.Get(node); sum.State == "done" || !sum.Archived.IsZero() {
		t.Fatalf("the view's node %+v", sum)
	}
}

// An invitation: the skill keeps one answer to it, on the invitation's
// item; one card, and its yes answers.
func TestInvitationReplyCard(t *testing.T) {
	c, fm, now := viewCore(t)
	invites := map[string]any{"resource": "monitor", "uri": "monitor://invitations", "items": map[string]any{"path": "invitations", "id": "id", "title": "title"}}
	d, err := c.nodes.Create(nodestore.New{Title: "Mes invitations", Lifetime: nodefile.Standing, Body: listBlock("attente", "invitations", invites), Author: "user"})
	if err != nil {
		t.Fatal(err)
	}
	node := d.Node.Meta.ID
	approvedMonitor(t, c, "invitations", monitorSpec{Way: wayPoll, Server: "monitor", Resource: "monitor://invitations", Every: 1, Cond: condNew, Path: "invitations", ID: "id"})
	addTriggered(t, c, "---\nname: invitations\ndescription: Les invitations.\nwhen: monitor.new key=invitations\ntools: [monitor]\nnode: "+node+"\n---\nPropose une réponse à chaque invitation.\n")
	lookAt(c, *now)
	fm.mu.Lock()
	fm.invites = []map[string]any{{"id": "i1", "title": "Point projet", "organizer": "sam@example.org"}}
	fm.mu.Unlock()
	lookAt(c, now.Add(time.Minute))
	c.tickSkills(context.Background(), now.Add(time.Minute))
	rs := runsOfSkill(t, c, "invitations")
	if len(rs) != 1 || c.taskNode(rs[0].Task) != node {
		m, _ := c.monitorNamed("invitations")
		t.Fatalf("runs %+v, events %+v, monitor %+v", rs, eventsOf(t, c, ""), m)
	}
	c.readNode(context.Background(), node, []string{"invitations"}, time.Now())
	ev := []EventRow{permission("r1", "mcp__monitor__respond", map[string]any{"id": "i1", "answer": "accept"})}
	c.store.addEvents(rs[0].Task, ev)
	c.applyPolicy(rs[0].Task, ev)
	endTurn(t, c, rs[0].Task, "Une invitation de Sam.")
	cards := cardsOf(c, cardCalls)
	if len(cards) != 1 {
		t.Fatalf("cards %+v", c.pendingProposals())
	}
	if asks := c.batchAsks(cards[0].ID); len(asks) != 1 || asks[0].Item != "i1" || asks[0].Node != node {
		t.Fatalf("asks %+v", asks)
	}
	if f, _ := json.Marshal(cards[0].FieldsIn("fr")); !strings.Contains(string(f), "accept") {
		t.Errorf("the card does not say the answer: %s", f)
	}
	if len(sentNow(fm)) != 0 {
		t.Fatal("answered before the yes")
	}
	if err := c.decideProposal(context.Background(), cardCalls, cards[0].ID, true); err != nil {
		t.Fatal(err)
	}
	if got := waitSent(t, fm, 1); got[0]["tool"] != "respond" || got[0]["answer"] != "accept" {
		t.Fatalf("sent %v", got)
	}
}

// A live block is read by code, with no model: its value is written in
// its snapshot with when; a read that fails keeps the last good value; a
// new one is written once the quarter of an hour is past.
func TestLiveBlockKeepsLastGood(t *testing.T) {
	c, fm, _ := viewCore(t)
	c.setServerTrust("monitor", true, "user")
	body := "```eriphe:block etat\n" + `{"components": {"component": "Text", "text": {"@path": "/web/data/state"}}, "sources": {"web": {"tool": "monitor/status", "args": {"service": "web"}}}}` + "\n```\n"
	d, err := c.nodes.Create(nodestore.New{Title: "La prod", Lifetime: nodefile.Standing, Body: body, Author: "user"})
	if err != nil {
		t.Fatal(err)
	}
	node := d.Node.Meta.ID
	tasks := func() int {
		var n int
		c.store.db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE kind != 'brain'`).Scan(&n)
		return n
	}
	before := tasks()
	start := time.Now()
	c.readNode(context.Background(), node, nil, start)
	snap := func() map[string]any {
		var m map[string]any
		json.Unmarshal(block(t, c, node, "etat").Snapshot, &m)
		return m
	}
	state := func(m map[string]any) any {
		web, _ := m["web"].(map[string]any)
		data, _ := web["data"].(map[string]any)
		return data["state"]
	}
	if s := snap(); state(s) != "up" || s[nodefile.SnapshotAt] == nil {
		t.Fatalf("snapshot %v", s)
	}
	if block(t, c, node, "etat").External {
		t.Error("a trusted server's block marked external")
	}

	// The server fails: the last good value stays, the error is said.
	fm.mu.Lock()
	fm.failing = true
	fm.mu.Unlock()
	c.readNode(context.Background(), node, []string{"web"}, start.Add(time.Minute))
	r, ok := c.nodeValue(node, "web")
	if !ok || r.Error == "" || state(map[string]any{"web": r.Value}) != "up" {
		t.Fatalf("read %+v", r)
	}
	if state(snap()) != "up" {
		t.Fatal("the snapshot lost its last good value")
	}

	// Back, changed: kept at once, written once the quarter is past.
	fm.mu.Lock()
	fm.failing, fm.state = false, "down"
	fm.mu.Unlock()
	c.readNode(context.Background(), node, []string{"web"}, start.Add(2*time.Minute))
	if r, _ := c.nodeValue(node, "web"); r.Error != "" || state(map[string]any{"web": r.Value}) != "down" {
		t.Fatalf("read %+v", r)
	}
	if state(snap()) != "up" {
		t.Fatal("a snapshot written within the quarter")
	}
	c.readNode(context.Background(), node, []string{"web"}, start.Add(nodeSnapshotEvery+3*time.Minute))
	if state(snap()) != "down" {
		t.Fatalf("snapshot %v", snap())
	}
	if tasks() != before {
		t.Fatal("a task ran to read a block")
	}
}

// A block whose source is a server not trusted is marked external by its
// first read, whoever wrote it.
func TestUntrustedSourceMarksExternal(t *testing.T) {
	c, _, _ := viewCore(t)
	body := "```eriphe:block etat\n" + `{"components": {"component": "Text", "text": {"@path": "/web/data/state"}}, "sources": {"web": {"tool": "monitor/status", "args": {"service": "web"}}}}` + "\n```\n"
	d, err := c.nodes.Create(nodestore.New{Title: "La prod", Lifetime: nodefile.Standing, Body: body, Author: "user"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Node.Block("etat").External {
		t.Fatal("external before its read")
	}
	c.readNode(context.Background(), d.Node.Meta.ID, nil, time.Now())
	if !block(t, c, d.Node.Meta.ID, "etat").External {
		t.Fatal("a block reading a server not trusted is not external")
	}
}

// A view's yes makes all three or none: a skill taken meanwhile takes
// back the node and the monitor, and the card still waits. No makes none.
// A view's blocks are checked as a task's: a source out of the catalog is
// refused before any card.
func TestViewAllOrNothing(t *testing.T) {
	c, _, _ := viewCore(t)
	in := map[string]any{
		"title":   "La prod",
		"body":    "```eriphe:block etat\n" + `{"components": {"component": "Text", "text": {"@path": "/cpu/value"}}, "sources": {"cpu": {"monitor": "cpu"}}}` + "\n```\n",
		"monitor": map[string]any{"name": "cpu", "server": "monitor", "tool": "status", "args": map[string]any{"service": "web"}, "cond": "cross", "path": "data.cpu", "above": 90},
		"skill":   map[string]any{"name": "prod", "description": "La prod.", "when": "monitor.crossed key=cpu", "instructions": "Regarde la prod."},
	}
	out, isErr := callBrainTool(t, c, "propose_view", in)
	if isErr {
		t.Fatal(out)
	}
	node := out["node"].(string)
	v := cardsOf(c, cardViewKind)[0]
	addTriggered(t, c, "---\nname: prod\ndescription: déjà là\n---\nAutre chose.\n")
	if err := c.decideProposal(context.Background(), cardViewKind, v.ID, true); err == nil {
		t.Fatal("a view made over a skill taken")
	}
	if sum, err := c.nodes.Get(node); err == nil && sum.Archived.IsZero() {
		t.Fatalf("the node stayed: %+v", sum)
	}
	if _, err := c.monitorNamed("cpu"); err == nil {
		t.Fatal("the monitor stayed")
	}
	if p := c.proposalOf(cardViewKind, v.ID); !p.Pending {
		t.Fatal("the card no longer waits")
	}
	if err := c.decideProposal(context.Background(), cardViewKind, v.ID, false); err != nil {
		t.Fatal(err)
	}
	if p := c.proposalOf(cardViewKind, v.ID); p.Pending || p.Outcome != cardRejected {
		t.Fatalf("after no %+v", p)
	}

	in["body"] = "```eriphe:block etat\n" + `{"components": {"component": "Text", "text": {"@path": "/x/v"}}, "sources": {"x": {"tool": "nowhere/read"}}}` + "\n```\n"
	in["monitor"].(map[string]any)["name"] = "cpu2"
	in["skill"].(map[string]any)["name"] = "prod2"
	if _, msg := callOn(t, connectServer(t, newMCPServer(c)), "propose_view", in); !strings.Contains(msg, "nowhere/read") {
		t.Fatalf("a source out of the catalog: %s", msg)
	}
}
