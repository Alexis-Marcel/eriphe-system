package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// nodeCore is a core with a device, a brain to tell, and the graph open.
func nodeCore(t *testing.T) *catalogCore {
	t.Helper()
	return brainCore(t, &BrainConfig{}, withGraph(t))
}

// callNodes calls one of eriphe-nodes' tools as a task: its result, or
// the error's text.
func callNodes(t *testing.T, c *core, task, name string, args map[string]any) (map[string]any, string) {
	t.Helper()
	tk, err := c.store.task(task)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	if _, err := c.taskNodesServer(tk).Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	sess, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		if tc, ok := res.Content[0].(*mcp.TextContent); ok {
			return nil, tc.Text
		}
		return nil, "error"
	}
	var out map[string]any
	raw, _ := json.Marshal(res.StructuredContent)
	json.Unmarshal(raw, &out)
	return out, ""
}

// mustNodes is callNodes that must succeed.
func mustNodes(t *testing.T, c *core, task, name string, args map[string]any) map[string]any {
	t.Helper()
	out, err := callNodes(t, c, task, name, args)
	if err != "" {
		t.Fatalf("%s: %s", name, err)
	}
	return out
}

// startInNode starts a worker task under a node of the graph.
func startInNode(t *testing.T, c *catalogCore, r runRequest) (*TaskRow, string) {
	t.Helper()
	task, err := c.runTask(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	id := c.taskNode(task.ID)
	if id == "" {
		t.Fatal("the task has no node")
	}
	return task, id
}

// A task works in a node of its own, a child of the node run_task names,
// running while it runs; it writes it as it goes (versions, trace
// entries), on the version it read.
func TestTaskWritesItsNode(t *testing.T) {
	c := nodeCore(t)
	parent, err := c.nodes.Create(nodestore.New{Title: "Substack", Author: "brain"})
	if err != nil {
		t.Fatal(err)
	}
	pid := parent.Node.Meta.ID
	task, id := startInNode(t, c, runRequest{Title: "Style des articles", Prompt: "Analyse le style.", Node: pid})
	sum, _ := c.nodes.Get(id)
	if sum.Parent != pid || sum.Title != "Style des articles" || c.nodeStatus(sum) != nodeRunning {
		t.Fatalf("its node: %+v %s", sum, c.nodeStatus(sum))
	}
	spec, _ := c.store.taskSpec(task.ID)
	if !strings.Contains(spec.AppendSystemPrompt, nodesServer) {
		t.Errorf("the task is not told of its node:\n%s", spec.AppendSystemPrompt)
	}
	read := mustNodes(t, c.core, task.ID, "node_read", nil)
	if read["id"] != id || read["version"].(float64) != 1 {
		t.Fatalf("read %v", read)
	}
	v2 := mustNodes(t, c.core, task.ID, "node_write", map[string]any{"body": "Trois articles lus.\n", "expect": read["hash"]})
	if v2["version"].(float64) != 2 {
		t.Fatalf("write %v", v2)
	}
	if _, err := callNodes(t, c.core, task.ID, "node_write", map[string]any{"body": "Écrit sur l'ancienne.", "expect": read["hash"]}); !strings.Contains(err, "changed since") {
		t.Fatalf("a stale write: %q", err)
	}
	mustNodes(t, c.core, task.ID, "node_append", map[string]any{"text": "Le ton est direct."})
	v3 := mustNodes(t, c.core, task.ID, "node_read", nil)
	mustNodes(t, c.core, task.ID, "node_edit", map[string]any{"expect": v3["hash"], "block": "summary",
		"value": map[string]any{"components": map[string]any{"component": "Text", "text": "Direct, court."}}})
	v5 := mustNodes(t, c.core, task.ID, "node_read", nil)
	mustNodes(t, c.core, task.ID, "node_edit", map[string]any{"expect": v5["hash"], "old": "Trois articles", "new": "Dix articles"})
	mustNodes(t, c.core, task.ID, "progress", map[string]any{"text": "Dix articles lus sur dix."})
	d, _ := c.nodes.Read(id, 0)
	if d.Version.N != 5 || !strings.Contains(d.Node.Text(), "Dix articles lus.") || !strings.Contains(d.Node.Text(), "Le ton est direct.") ||
		d.Node.Block("summary") == nil || d.Version.Author != "task:"+task.ID {
		t.Fatalf("v%d by %s:\n%s", d.Version.N, d.Version.Author, d.Raw)
	}
	if trace, _ := c.nodes.Trace(id); !strings.Contains(string(trace), "Dix articles lus sur dix.") {
		t.Fatalf("trace:\n%s", trace)
	}
	// It reads the graph, its parent too (where else it writes:
	// task_write_test.go).
	if p := mustNodes(t, c.core, task.ID, "node_read", map[string]any{"id": pid}); p["title"] != "Substack" {
		t.Fatalf("its parent read: %v", p)
	}
	// A child of its node, under it.
	kid := mustNodes(t, c.core, task.ID, "node_open", map[string]any{"title": "Plan", "goal": "Un plan d'article.", "body": "À faire."})
	if k, _ := c.nodes.Get(kid["id"].(string)); k.Parent != id {
		t.Fatalf("child %+v", k)
	}
	// Its status follows the task's: stopped, it is idle, not done (only
	// the user says a node is done).
	c.store.updateTask(task.ID, map[string]any{"state": stateStopped})
	c.taskNodeMoved(task.ID, stateStopped)
	if s := nodeStatusNow(t, c.core, id); s != nodeIdle {
		t.Fatalf("status %s", s)
	}
	// A node that is not there, or archived, is refused before the start.
	if _, err := c.runTask(context.Background(), runRequest{Prompt: "x", Node: "nope"}); err == nil {
		t.Fatal("started under no node")
	}
	c.nodes.Archive(pid, "user")
	if _, err := c.runTask(context.Background(), runRequest{Prompt: "x", Node: pid}); err == nil {
		t.Fatal("started under an archived node")
	}
}

// A task that wrote nothing leaves its report as its doc, whole, a long
// one too; its trace holds it whole; the brain is woken in a few lines
// that point to the node. The same turn is not filed twice.
func TestTaskReportFiled(t *testing.T) {
	c := nodeCore(t)
	task, id := startInNode(t, c, runRequest{Title: "Recherche sur l'erreur", Prompt: "Cherche."})
	var b strings.Builder
	b.WriteString("L'erreur vient du cache.\n\n")
	for b.Len() < 50_000 {
		b.WriteString("Une ligne de détail sur ce que la recherche a trouvé, avec https://github.com/Some-Owner/repo/pull/640/files. ")
	}
	b.WriteString("FIN.")
	report := b.String()
	receipt := "\n\n```json\n" + `{"receipt": {"status": "done", "summary": "Trouvé"}}` + "\n```"
	idleTask(t, c.core, task.ID, ev(1, "text", map[string]any{"text": report + receipt}), ev(2, "turn_end", map[string]any{"subtype": "success"}))
	idleTask(t, c.core, task.ID, ev(3, "text", map[string]any{"text": "Merci." + receipt}), ev(4, "turn_end", map[string]any{"subtype": "success"}))

	trace, _ := c.nodes.Trace(id)
	if !strings.Contains(string(trace), report) {
		t.Fatalf("the report is not whole in the trace (%d bytes)", len(trace))
	}
	d, _ := c.nodes.Read(id, 0)
	if strings.TrimSpace(d.Node.Text()) != report {
		t.Fatalf("the doc is not the report (%d bytes)", len(d.Node.Text()))
	}
	if s := nodeStatusNow(t, c.core, id); s != nodeIdle {
		t.Fatalf("status %s", s)
	}
	// The doc written, the next report goes to the trace only.
	if !strings.Contains(string(trace), "Merci.") || strings.Contains(d.Node.Text(), "Merci.") {
		t.Fatalf("the second report:\n%s", d.Node.Text())
	}
	msgs, _ := c.store.undelivered()
	var wake string
	for _, m := range msgs {
		if strings.Contains(m.Text, "Recherche sur l'erreur") && strings.Contains(m.Text, "cache") {
			wake = m.Text
		}
	}
	if wake == "" || utf8.RuneCountInString(wake) > 600 || !strings.Contains(wake, "nœud "+id) || !strings.Contains(wake, "L'erreur vient du cache.") || strings.Contains(wake, "FIN.") {
		t.Fatalf("wake %q", wake)
	}
	// Filed once: the same turn heard again adds nothing.
	before, _ := c.nodes.Trace(id)
	c.notifyBrain(task.ID, stateIdle)
	if after, _ := c.nodes.Trace(id); len(after) != len(before) {
		t.Fatal("filed twice")
	}
}

// A task that wrote its doc keeps it: its report goes to the trace.
func TestTaskReportKeepsTheDoc(t *testing.T) {
	c := nodeCore(t)
	task, id := startInNode(t, c, runRequest{Title: "Plan", Prompt: "Écris le plan."})
	read := mustNodes(t, c.core, task.ID, "node_read", nil)
	mustNodes(t, c.core, task.ID, "node_write", map[string]any{"body": "1. Titre\n2. Corps\n", "expect": read["hash"]})
	idleTask(t, c.core, task.ID, ev(1, "text", map[string]any{"text": "Plan écrit dans le nœud." + doneReceipt[len("Fixed the form."):]}), ev(2, "turn_end", map[string]any{"subtype": "success"}))
	d, _ := c.nodes.Read(id, 0)
	if d.Node.Text() != "1. Titre\n2. Corps\n" {
		t.Fatalf("doc %q", d.Node.Text())
	}
	if trace, _ := c.nodes.Trace(id); !strings.Contains(string(trace), "Plan écrit dans le nœud.") {
		t.Fatalf("trace %s", trace)
	}
}

// A report with a secret is not filed; the brain hears it was not, and
// still hears of the turn.
func TestTaskReportRefused(t *testing.T) {
	c := nodeCore(t)
	task, id := startInNode(t, c, runRequest{Title: "Clés", Prompt: "x"})
	idleTask(t, c.core, task.ID, ev(1, "text", map[string]any{"text": "La clé : ghp_0123456789abcdefghijABCDEFGHIJ" + doneReceipt[len("Fixed the form."):]}), ev(2, "turn_end", map[string]any{"subtype": "success"}))
	if trace, _ := c.nodes.Trace(id); len(trace) != 0 {
		t.Fatalf("trace %s", trace)
	}
	if msg := lastEvent(t, c.core); !strings.Contains(msg, "pas été rangé") || !strings.Contains(msg, "task_status") {
		t.Fatalf("wake %q", msg)
	}
}

// A tainted task writes external: at start, or once it read someone
// else's content; its node and the blocks it writes are marked by code;
// a block it writes then asks nothing.
func TestTaintedTaskWritesExternal(t *testing.T) {
	c := nodeCore(t)
	task, id := startInNode(t, c, runRequest{Title: "Mail de Sam", Prompt: "Trie.", taint: []string{"mail"}})
	if sum, _ := c.nodes.Get(id); !sum.External {
		t.Fatal("a task started tainted has a clean node")
	}
	clean, cid := startInNode(t, c, runRequest{Title: "Propre", Prompt: "x"})
	read := mustNodes(t, c.core, clean.ID, "node_read", nil)
	block := map[string]any{"components": map[string]any{"component": "Text", "text": "Sam écrit."}}
	mustNodes(t, c.core, clean.ID, "node_edit", map[string]any{"expect": read["hash"], "block": "mail", "value": block})
	if d, _ := c.nodes.Read(cid, 0); d.Node.Meta.External || d.Node.Block("mail").External {
		t.Fatalf("a clean write is external:\n%s", d.Raw)
	}
	c.taskRead(clean.ID, "mail")
	read = mustNodes(t, c.core, clean.ID, "node_read", nil)
	mustNodes(t, c.core, clean.ID, "node_edit", map[string]any{"expect": read["hash"], "block": "reply", "value": block})
	d, _ := c.nodes.Read(cid, 0)
	if !d.Node.Meta.External || !d.Node.Block("reply").External || d.Node.Block("mail").External {
		t.Fatalf("after reading a mail:\n%s", d.Raw)
	}
	// Tainted, a block still asks in its author's words, marked external.
	ask := map[string]any{"components": map[string]any{"component": "Button", "label": "Envoyer",
		"action": map[string]any{"event": map[string]any{"name": "ask", "context": map[string]any{"text": "Envoie la réponse."}}}}}
	read = mustNodes(t, c.core, task.ID, "node_read", nil)
	mustNodes(t, c.core, task.ID, "node_edit", map[string]any{"expect": read["hash"], "block": "send", "value": ask})
	if d, _ := c.nodes.Read(id, 0); d.Node.Block("send") == nil || !d.Node.Block("send").External {
		t.Fatalf("a tainted ask:\n%s", d.Raw)
	}
	// A child it opens tainted is external.
	kid := mustNodes(t, c.core, task.ID, "node_open", map[string]any{"goal": "Un but.", "title": "Réponse à Sam", "body": "Brouillon."})
	if k, _ := c.nodes.Get(kid["id"].(string)); !k.External {
		t.Fatal("a tainted task's child is clean")
	}
	// An item is noted on one of its sources only.
	if err := c.noteItem(id, itemNote{Source: "x", Item: "1", Note: "n", Author: "brain"}); err == nil {
		t.Fatal("noted on no source")
	}
}

// What a task's blocks may hold: no component outside the catalog, no Link opening a
// source's value, a source of a tool it is lent only; a source of a
// server not trusted marks the block external.
func TestTaskBlocksGuarded(t *testing.T) {
	c := nodeCore(t)
	task, id := startInNode(t, c, runRequest{Title: "Docs", Prompt: "x", Resources: []ResourceRef{{Kind: "tool", Ref: "docs"}}})
	write := func(value map[string]any) string {
		t.Helper()
		read := mustNodes(t, c.core, task.ID, "node_read", nil)
		_, err := callNodes(t, c.core, task.ID, "node_edit", map[string]any{"expect": read["hash"], "block": "b", "value": value})
		return err
	}
	for name, value := range map[string]map[string]any{
		"offers": {"components": map[string]any{"component": "Offers", "offers": map[string]any{"@path": "/x"}}, "data": map[string]any{"x": []any{}}},
		"link to a source": {"components": map[string]any{"component": "Link", "label": "Ouvrir", "url": map[string]any{"@path": "/page/url"}},
			"sources": map[string]any{"page": map[string]any{"tool": "docs/read"}}},
		"a tool not lent": {"components": map[string]any{"component": "Text", "text": map[string]any{"@path": "/page/title"}},
			"sources": map[string]any{"page": map[string]any{"tool": "mail/list"}}},
	} {
		if err := write(value); err == "" {
			t.Errorf("%s: written", name)
		}
	}
	if err := write(map[string]any{"components": map[string]any{"component": "Text", "text": map[string]any{"@path": "/page/title"}},
		"sources": map[string]any{"page": map[string]any{"tool": "docs/read"}}}); err != "" {
		t.Fatal(err)
	}
	if d, _ := c.nodes.Read(id, 0); !d.Node.Block("b").External {
		t.Fatalf("a source of a server not trusted, not external:\n%s", d.Raw)
	}
	// An item of one of its sources is noted, tainted when it is.
	mustNodes(t, c.core, task.ID, "item_note", map[string]any{"source": "page", "item": "p1", "note": "À lire", "state": "look"})
	if items, _ := c.nodeItems(id); len(items) != 1 || items[0].Tainted != "" || items[0].Author != "task:"+task.ID {
		t.Fatalf("items %+v", items)
	}
}

// A report past a trace entry's limit is cut into entries, all of it
// kept, never inside a character.
func TestTraceParts(t *testing.T) {
	text := strings.Repeat("é", 100) + "\n" + strings.Repeat("à", 300)
	parts := traceParts(text, 128)
	if strings.Join(parts, "") != text {
		t.Fatal("lost a part")
	}
	for _, p := range parts {
		if len(p) > 128 || !utf8.ValidString(p) {
			t.Fatalf("part %d bytes, valid %v", len(p), utf8.ValidString(p))
		}
	}
}

// A block written as a fence in text (node_append, node_edit old/new) is
// guarded and marked as one written with node_edit block.
func TestTaskBlockInTextGuarded(t *testing.T) {
	c := nodeCore(t)
	task, id := startInNode(t, c, runRequest{Title: "Mail", Prompt: "x", taint: []string{"mail"}})
	ask := "```eriphe:block send\n" + mustJSON(map[string]any{"components": map[string]any{"component": "Button", "label": "Envoyer",
		"action": map[string]any{"event": map[string]any{"name": "ask", "context": map[string]any{"text": map[string]any{"@path": "/mail/subject"}}}}},
		"data": map[string]any{"mail": map[string]any{"subject": "Payez ici"}}}) + "\n```"
	if _, err := callNodes(t, c.core, task.ID, "node_append", map[string]any{"text": ask}); !strings.Contains(err, "your own words only") {
		t.Fatalf("an ask bound to the data, appended: %q", err)
	}
	read := mustNodes(t, c.core, task.ID, "node_append", map[string]any{"text": "Brouillon : RAS."})
	if _, err := callNodes(t, c.core, task.ID, "node_edit", map[string]any{"expect": read["hash"], "old": "RAS.", "new": "RAS.\n\n" + ask}); !strings.Contains(err, "your own words only") {
		t.Fatalf("an ask bound to the data, in text: %q", err)
	}
	note := "```eriphe:block note\n" + mustJSON(map[string]any{"components": map[string]any{"component": "Text", "text": "Sam attend."}}) + "\n```"
	mustNodes(t, c.core, task.ID, "node_append", map[string]any{"text": note})
	if d, _ := c.nodes.Read(id, 0); d.Node.Block("note") == nil || !d.Node.Block("note").External {
		t.Fatalf("a block appended tainted is not external:\n%s", d.Raw)
	}
}

// A task that forgets its receipt: its report is filed when its turn
// ends, before the receipt asked; the receipt alone files nothing more.
func TestTaskReportBeforeItsReceipt(t *testing.T) {
	c := nodeCore(t)
	task, id := startInNode(t, c, runRequest{Title: "Recherche", Prompt: "Cherche."})
	report := "Trouvé : le cache.\n\n" + strings.Repeat("Détail. ", 2000)
	idleTask(t, c.core, task.ID, ev(1, "text", map[string]any{"text": report}), ev(2, "turn_end", map[string]any{"subtype": "success"}))
	if items, _ := c.store.inbox(task.ID); len(items) != 1 || items[0].Data["text"] != receiptRepair {
		t.Fatalf("no receipt asked: %+v", items)
	}
	d, _ := c.nodes.Read(id, 0)
	if strings.TrimSpace(d.Node.Text()) != strings.TrimSpace(report) {
		t.Fatalf("the report is not its doc: %q", truncate(d.Node.Text(), 80))
	}
	before, _ := c.nodes.Trace(id)
	idleTask(t, c.core, task.ID, ev(3, "text", map[string]any{"text": "```json\n" + `{"receipt": {"status": "done", "summary": "Trouvé"}}` + "\n```"}), ev(4, "turn_end", map[string]any{"subtype": "success"}))
	if after, _ := c.nodes.Trace(id); string(after) != string(before) {
		t.Fatalf("the receipt filed again:\n%s", after[len(before):])
	}
}

// A task's calls to its node never taint it: what it reads there taints
// by its own record.
func TestNodeCallsDoNotTaint(t *testing.T) {
	c := nodeCore(t)
	task, _ := startInNode(t, c, runRequest{Title: "Propre", Prompt: "x"})
	c.store.addEvents(task.ID, []EventRow{ev(1, "tool_use", map[string]any{"name": "mcp__eriphe-nodes__node_write", "input": map[string]any{"body": "x"}})})
	if why := c.taskTaint(task.ID); len(why) != 0 {
		t.Fatalf("tainted by its node: %v", why)
	}
}

// A title or a goal the filter refuses does not stop the task: its node
// goes with neutral ones (every node has a goal).
func TestTaskNodeRefusedGoal(t *testing.T) {
	c := nodeCore(t)
	_, id := startInNode(t, c, runRequest{Title: "Le jeton", Prompt: "Renouvelle le token: expiré-depuis-hier, puis dis-le."})
	if d, _ := c.nodes.Read(id, 0); !strings.HasPrefix(d.Node.Meta.Goal, "Le travail de la tâche ") || d.Node.Meta.Title != "Le jeton" {
		t.Fatalf("node %+v", d.Node.Meta)
	}
	task, id := startInNode(t, c, runRequest{Title: "password: hunter2hunter2", Prompt: "x"})
	if d, _ := c.nodes.Read(id, 0); d.Node.Meta.Title != "Tâche "+task.ID {
		t.Fatalf("node %+v", d.Node.Meta)
	}
}

// A node is marked external even when someone writes it between the mark's
// read and its write: the mark is tried again on the new version, and the
// write made meanwhile is kept.
func TestExternalMarkSurvivesAConcurrentWrite(t *testing.T) {
	c := graphCore(t)
	d, err := c.nodes.Create(nodestore.New{Title: "Réponse à Sam", Body: "Brouillon.", Author: byBrain})
	if err != nil {
		t.Fatal(err)
	}
	id := d.Node.Meta.ID
	first := true
	markRead = func(string) {
		if first {
			first = false
			if _, err := nodeWrite(c, id, "Écrit entre-temps.", d.Version.Hash, byUser); err != nil {
				t.Error(err)
			}
		}
	}
	t.Cleanup(func() { markRead = nil })
	c.markNodeExternal(id, byBrain)
	got, _ := c.nodes.Read(id, 0)
	if !got.Node.Meta.External {
		t.Fatal("the mark was lost")
	}
	if got.Node.Text() != "Écrit entre-temps." {
		t.Errorf("the write made meanwhile: %q", got.Node.Text())
	}
}
