package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Only the brain is budgeted. A worker (a skill's run among them) never
// reaches eriphe-core's own tools, where the budget is held: the relay
// refuses it. A task's own tools (eriphe-nodes) are another server, never
// budgeted: a task writes as much as its work needs.
func TestBrainBudgetIsTheBrainsOnly(t *testing.T) {
	c := nodeCore(t)
	c.self = newMCPServer(c.core)
	c.store.insertTask(&TaskRow{ID: "w-1", Kind: "worker", Device: "pc", Runtime: "claude-code", State: stateRunning})
	mux := http.NewServeMux()
	mux.Handle("/v1/device/tasks/{id}/mcp/{server}", c.taskMCPHandler())
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c.store.insertTask(&TaskRow{ID: "b-1", Kind: "brain", Device: "pc", Runtime: "claude-code", State: stateRunning})
	setBrainTask(c.core, "b-1")
	dial := func(task string) (*mcp.ClientSession, error) {
		tr := &mcp.StreamableClientTransport{Endpoint: srv.URL + "/v1/device/tasks/" + task + "/mcp/" + selfServer,
			HTTPClient: &http.Client{Transport: headerTransport{"Authorization": "Bearer device-secret", "Eriphe-Device": "pc"}}, MaxRetries: -1}
		return mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(context.Background(), tr, nil)
	}
	// The brain's task, on the same server and device, reaches them and
	// is held: the others' refusal is theirs, not the setup's.
	brain, err := dial("b-1")
	if err != nil {
		t.Fatalf("the brain's task: %v", err)
	}
	defer brain.Close()
	c.turn.start(1, 1, 0)
	if _, msg := callOn(t, brain, "node_open", map[string]any{"title": "Page", "goal": "Une page.",
		"body": strings.Repeat("Une ligne. ", brainWriteRunes/10)}); !strings.HasPrefix(msg, "Refused") {
		t.Fatalf("the brain's whole page: %q", msg)
	}
	for _, task := range []string{"w-1"} { // a worker (a skill's run is one)
		if cs, err := dial(task); err == nil {
			cs.Close()
			t.Fatalf("task %s reached eriphe-core's own tools", task)
		}
	}
	page := mustNode(t, c.core, nodestore.New{Title: "Mes mails", Body: "v0", Author: byBrain})
	task, _ := startInNode(t, c, runRequest{Title: "Tri", Prompt: "Trie.", Node: page})
	c.turn.start(1, 1, 0)
	for i := range brainNodeWrites + 3 {
		if _, msg := callNodes(t, c.core, task.ID, "node_edit", map[string]any{"id": page,
			"old": "v" + strconv.Itoa(i), "new": "v" + strconv.Itoa(i+1)}); msg != "" {
			t.Fatalf("a task's edit %d: %s", i+1, msg)
		}
	}
}

// Each of the brain's tools has its class in the budget: a tool added
// without one would be counted as a change, unseen.
func TestBrainBudgetClassifiesTools(t *testing.T) {
	res, err := connectServer(t, newMCPServer(graphCore(t))).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, tl := range res.Tools {
		listed[tl.Name] = true
		if _, ok := brainToolClass[tl.Name]; !ok {
			t.Errorf("%s has no class in brainToolClass (brain_budget.go)", tl.Name)
		}
	}
	for name := range brainToolClass {
		if !listed[name] {
			t.Errorf("brainToolClass names %s, which the brain does not have", name)
		}
	}
}

// refusal is a charge's refusal, "" when it may run.
func refusal(why string, _ spent) string { return why }

// refusedFor tells a budget's refusal: its words, with the hint to launch
// a task.
func refusedFor(t *testing.T, why string) {
	t.Helper()
	if !strings.HasPrefix(why, "Refused") {
		t.Fatalf("not refused by the budget: %q", why)
	}
	if !strings.Contains(why, "skill is proposed per turn") && !strings.Contains(why, "run_task with node, skill") {
		t.Errorf("no hint to launch a task: %q", why)
	}
}

// The budget of one turn: reads never count; a whole page, a series of
// node writes, a second skill and too many changes are
// refused, each with the hint; what routes and launches always passes.
func TestTurnBudget(t *testing.T) {
	page := strings.Repeat("Une ligne de la page, ses titres et ses listes.\n", 20)
	line := map[string]any{"id": "n1", "expect": "h", "body": "En cours : je m'en occupe."}

	b := newTurnBudget(1)
	for range 50 {
		for _, read := range []string{"node_read", "node_find", "recall", "list_skills", "since", "task_status", "page_format"} {
			if why := refusal(b.charge(read, map[string]any{"id": "n1"})); why != "" {
				t.Fatalf("%s refused: %s", read, why)
			}
		}
	}
	// A whole page: refused, by node_write, node_open or node_edit.
	refusedFor(t, refusal(b.charge("node_write", map[string]any{"id": "n1", "expect": "h", "body": page})))
	refusedFor(t, refusal(b.charge("node_open", map[string]any{"title": "Mes mails", "goal": "Un but.", "body": page})))
	refusedFor(t, refusal(b.charge("node_edit", map[string]any{"id": "n1", "old": "x", "new": page})))
	// A work cut in steps is orchestration: its steps are not page text.
	steps := []any{map[string]any{"key": "style", "do": page}, map[string]any{"key": "plan", "do": page}}
	if why := refusal(b.charge("node_open", map[string]any{"title": "Premier article", "goal": "Un plan.", "body": "En cours.", "steps": steps})); why != "" {
		t.Fatalf("a work in steps refused: %s", why)
	}
	// A short write passes, three of them; the fourth is a rewrite.
	for i := range brainNodeWrites {
		name := []string{"node_write", "node_edit", "node_write"}[i]
		if why := refusal(b.charge(name, line)); why != "" {
			t.Fatalf("write %d refused: %s", i+1, why)
		}
	}
	refusedFor(t, refusal(b.charge("node_edit", line)))

	// One skill a turn; a monitor tried and written are changes.
	b = newTurnBudget(1)
	if why := refusal(b.charge("write_skill", map[string]any{"name": "mails"})); why != "" {
		t.Fatalf("a skill refused: %s", why)
	}
	refusedFor(t, refusal(b.charge("write_skill", map[string]any{"name": "jour"})))
	for _, name := range []string{"try_monitor", "write_monitor", "remember", "node_set", "notify"} {
		if why := refusal(b.charge(name, nil)); why != "" {
			t.Fatalf("%s refused: %s", name, why)
		}
	}
	// Six changes made: the seventh, of any kind, catalog included, is work.
	refusedFor(t, refusal(b.charge("remember", nil)))
	refusedFor(t, refusal(b.chargeCatalog()))
	// The way out is always open.
	for _, name := range []string{"run_task", "node_show", "turn_home", "answer_task", "stop_task", "withdraw_proposal", "node_settle"} {
		if why := refusal(b.charge(name, map[string]any{"node": "n1", "ask": "Fais avancer ce nœud."})); why != "" {
			t.Fatalf("%s refused when the budget is spent: %s", name, why)
		}
	}
	if why := refusal(b.charge("node_open", map[string]any{"title": "Mes mails", "goal": "Un but.", "body": "En cours : je m'en occupe."})); why != "" {
		t.Fatalf("opening a node refused when the budget is spent: %s", why)
	}

	// A delivery of three messages (three tasks ended) may update their
	// three nodes and their parents; never more than three times the budget.
	b = newTurnBudget(3)
	for i := range brainNodeWrites * 3 {
		if why := refusal(b.charge("node_edit", line)); why != "" {
			t.Fatalf("edit %d of a delivery of three refused: %s", i+1, why)
		}
	}
	if newTurnBudget(10).scale != brainBudgetScale {
		t.Errorf("a delivery of ten scales the budget by %d", newTurnBudget(10).scale)
	}

	// A page split in pieces is a page: the turn's node text is held.
	b = newTurnBudget(1)
	half := map[string]any{"id": "n1", "expect": "h", "old": "x", "new": strings.Repeat("é", brainWriteRunes/2+50)}
	if why := refusal(b.charge("node_edit", half)); why != "" {
		t.Fatalf("half a page refused: %s", why)
	}
	refusedFor(t, refusal(b.charge("node_edit", half)))

	// A call that failed changed nothing: what it took comes back.
	b = newTurnBudget(1)
	for range 5 {
		_, took := b.charge("node_edit", line)
		b.refund(took) // stale: read again, and made again
	}
	for i := range brainNodeWrites {
		if why := refusal(b.charge("node_edit", line)); why != "" {
			t.Fatalf("write %d after failed ones refused: %s", i+1, why)
		}
	}
	b = newTurnBudget(1)
	b.refund(spent{class: budgetWrite, runes: 100}) // taken from an earlier delivery's
	if b.gestures != 0 || b.nodeWrites != 0 || b.runes != 0 {
		t.Errorf("a refund went below nothing: %+v", b)
	}
}

// A delivery's budget is scaled by the messages it brings: three tasks'
// ends delivered together may update their three nodes.
func TestBrainDeliveryScalesBudget(t *testing.T) {
	c, _ := testMemoryCore(t)
	setBrain(c, &BrainConfig{Agent: "small"})
	ctx := context.Background()
	if err := tendBrain(ctx, c); err != nil {
		t.Fatal(err)
	}
	c.store.updateTask("t-brain", map[string]any{"state": stateIdle})
	for _, text := range []string{"La tâche A a fini.", "La tâche B a fini.", "La tâche C a fini."} {
		if _, err := c.store.addMessage(roleEvent, text, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := tendBrain(ctx, c); err != nil {
		t.Fatal(err)
	}
	c.turn.mu.Lock()
	scale := c.turn.budget.scale
	c.turn.mu.Unlock()
	if scale != 3 {
		t.Fatalf("a delivery of three events: budget scaled by %d", scale)
	}
}

// A turn that answers the user's yes on a card keeps what it needs: what
// came of it said in its node and its parent, the node done, another card
// withdrawn, the next work launched.
func TestTurnBudgetCardFollowUp(t *testing.T) {
	c := graphCore(t)
	c.turn.start(1, 1, 0)
	c.turn.goOn(2, 2, 0, nil) // the card's outcome, delivered as an event
	c.turn.answersUser()
	c.turn.delivered(1)
	line := map[string]any{"id": "n1", "expect": "h", "old": "attend ton oui", "new": "C'est envoyé."}
	for _, call := range []struct {
		name string
		args map[string]any
	}{
		{"node_read", map[string]any{"id": "n1"}},
		{"node_edit", line},
		{"node_edit", map[string]any{"id": "parent", "old": "Reste à", "new": "Fait :"}},
		{"node_set", map[string]any{"id": "n1", "done": true}},
		{"withdraw_proposal", map[string]any{"ref": "calls:2"}},
		{"run_task", map[string]any{"node": "n1", "ask": "Fais avancer ce nœud."}},
	} {
		if why := refusal(c.turn.charge(call.name, call.args)); why != "" {
			t.Fatalf("a card's follow-up: %s refused: %s", call.name, why)
		}
	}
}

// The brain's catalog calls that change something count in its budget;
// its reads never do, and a worker's calls are never budgeted.
func TestBrainBudgetCountsCatalogChanges(t *testing.T) {
	c := newCatalogCore(t, withGraph(t))
	setBrain(c, &BrainConfig{})
	editBrain(c, func(a *Assistant) { a.Tools = []string{"github"} })
	c.store.insertTask(&TaskRow{ID: "b-1", Device: "pc", Kind: "brain", Runtime: "claude-code", State: stateRunning})
	setBrainTask(c, "b-1")
	c.turn.start(1, 1, 0)
	ctx := context.Background()
	cs := connectTo(t, c.taskServer("b-1", c.brainGrant(), "github"))
	args := map[string]any{"title": "x"}
	create := func(n int) *mcp.CallToolResult {
		c.store.recordDecision("b-1", "p"+strconv.Itoa(n), "mcp__github__create_issue", args, decideAllow, byUser, "")
		out, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "create_issue", Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	for i := range brainGestures {
		if out := create(i); out.IsError {
			t.Fatalf("change %d = %s", i+1, resultText(out))
		}
		if out, _ := cs.CallTool(ctx, &mcp.CallToolParams{Name: "get_issue", Arguments: map[string]any{"number": 3}}); out.IsError {
			t.Fatalf("free read = %s", resultText(out))
		}
	}
	refusedFor(t, resultText(create(brainGestures)))
	// A worker's calls are its own: no budget.
	c.store.insertTask(&TaskRow{ID: "t-1", Device: "pc", Runtime: "claude-code", State: stateRunning, Tools: "github"})
	w := connectTo(t, c.taskServer("t-1", []string{"github"}, "github"))
	for i := range brainGestures + 1 {
		c.store.recordDecision("t-1", "w"+strconv.Itoa(i), "mcp__github__create_issue", args, decideAllow, byUser, "")
		if out, _ := w.CallTool(ctx, &mcp.CallToolParams{Name: "create_issue", Arguments: args}); out.IsError {
			t.Fatalf("a worker's change %d = %s", i+1, resultText(out))
		}
	}
}

// The budget holds the brain's calls on both of its ways to eriphe-core's
// own server, a fresh delivery starts a fresh budget, and the owner's own
// client with the API token is not the brain: never counted.
func TestBrainBudgetHoldsTheBrainOnly(t *testing.T) {
	c := newCatalogCore(t, withGraph(t))
	setBrain(c, &BrainConfig{})
	c.token = "api-token"
	c.self = newMCPServer(c)
	c.store.registerDevice("pc", hashSecret("device-secret"))
	c.store.insertTask(&TaskRow{ID: "b-1", Kind: "brain", Device: "pc", Runtime: "claude-code", State: stateRunning})
	setBrainTask(c, "b-1")
	mux := http.NewServeMux()
	mux.Handle("/v1/device/tasks/{id}/mcp/{server}", c.taskMCPHandler())
	mux.Handle("/mcp", selfMCPHandler(c.self, c.token))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	connect := func(endpoint string, headers headerTransport) *mcp.ClientSession {
		tr := &mcp.StreamableClientTransport{Endpoint: srv.URL + endpoint, HTTPClient: &http.Client{Transport: headers}, MaxRetries: -1}
		cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(context.Background(), tr, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cs.Close() })
		return cs
	}
	relayed := connect("/v1/device/tasks/b-1/mcp/"+selfServer, headerTransport{"Authorization": "Bearer device-secret", "Eriphe-Device": "pc"})
	selfURL := connect("/mcp", headerTransport{"Authorization": "Bearer " + assistantToken(c.token)})
	owners := connect("/mcp", headerTransport{"Authorization": "Bearer api-token", brainCallHeader: "0"})
	d, err := c.nodes.Create(nodestore.New{Title: "Devis", Goal: "Un devis.", Body: "v0", Author: "brain"})
	if err != nil {
		t.Fatal(err)
	}
	id, word := d.Node.Meta.ID, 0
	// edit replaces the node's word (old, when it is not "") by the
	// next: the error's words, "" when it was written.
	edit := func(cs *mcp.ClientSession, old string) string {
		d, err := c.nodes.Read(id, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, msg := callOn(t, cs, "node_edit", map[string]any{"id": id,
			"old": cmpOr(old, strings.TrimSpace(d.Node.Text())), "new": "v" + strconv.Itoa(word+1)})
		if msg == "" {
			word++
		}
		return msg
	}
	for name, cs := range map[string]*mcp.ClientSession{"relayed by its device": relayed, "at self_url": selfURL} {
		c.turn.start(1, 1, 0)
		// An edit of a line gone fails: it costs nothing.
		if msg := edit(cs, "gone"); msg == "" || strings.HasPrefix(msg, "Refused") {
			t.Fatalf("the brain %s: an edit of a line gone: %q", name, msg)
		}
		for i := range brainNodeWrites {
			if msg := edit(cs, ""); msg != "" {
				t.Fatalf("the brain %s: edit %d: %s", name, i+1, msg)
			}
		}
		refusedFor(t, edit(cs, ""))
		// Reads go on; a fresh delivery has a fresh budget.
		if _, msg := callOn(t, cs, "node_read", map[string]any{"id": id}); msg != "" {
			t.Fatalf("a read after the budget: %s", msg)
		}
		c.turn.goOn(2, 2, 0, nil)
		if msg := edit(cs, ""); msg != "" {
			t.Fatalf("the brain %s: the next delivery's first edit: %s", name, msg)
		}
	}
	c.turn.start(3, 3, 0)
	for i := range brainNodeWrites + 2 {
		if msg := edit(owners, ""); msg != "" {
			t.Fatalf("the owner's own client, edit %d: %s", i+1, msg)
		}
	}
}
