package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// workIn makes a task in state work in node id, as a task opened there
// does; it gives the task's id.
func workIn(t *testing.T, c *core, id, state string) string {
	t.Helper()
	task, err := taskIn(c, id, state)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func taskIn(c *core, id, state string) (string, error) {
	task := fmt.Sprintf("t-%s-%d", id, time.Now().UnixNano())
	if err := c.store.insertTask(&TaskRow{ID: task, Device: "d", Runtime: "claude", Dir: "/work", Prompt: "p", State: state}); err != nil {
		return "", err
	}
	_, err := c.store.db.Exec(`INSERT INTO task_nodes (task, node) VALUES (?, ?)`, task, id)
	return task, err
}

// setStatus gives node id a status as code would: a step running (no
// task listed beside the test's own), a card waiting on it,
// its file done; nothing for idle.
func setStatus(t *testing.T, c *core, id, status string) {
	t.Helper()
	if err := fakeStatus(c, id, status); err != nil {
		t.Fatal(err)
	}
}

func fakeStatus(c *core, id, status string) error {
	var err error
	switch status {
	case nodeRunning:
		_, err = c.store.db.Exec(`INSERT INTO node_steps (node, parent, n, key, spec, status, opener) VALUES (?, 'elsewhere', 1, 'k', '{}', ?, 'brain')`, id, stepRunning)
	case nodeWaits: // a card waits on it: the one way a node waits for the user
		// A card of no kind of proposals: the status counts it, nothing
		// settles it, and no page draws it (it has nothing to approve).
		var n int64
		c.store.db.QueryRow(`SELECT COUNT(*) FROM convo_cards`).Scan(&n)
		ref := fmt.Sprintf("fake:%d", n+1)
		if _, err = c.store.db.Exec(`INSERT INTO convo_cards (ref, kind, text, created) VALUES (?, 'fake', 'x', 1)`, ref); err == nil {
			_, err = c.store.db.Exec(`INSERT INTO node_cards (node, ref, created) VALUES (?, ?, 1)`, id, ref)
		}
	case nodeDone:
		_, err = c.nodes.SetDone(id, true, byUser)
	}
	c.nodeMoved(id, false)
	return err
}

// cardWaitsOn tells a node a waiting card is linked to.
func cardWaitsOn(c *core, id string) bool { return c.statusFacts(id).cards[id] }

// nodeStatusNow is a node's status now.
func nodeStatusNow(t *testing.T, c *core, id string) string {
	t.Helper()
	sum, err := c.nodes.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return c.nodeStatus(sum)
}

// A node's status, over every case it is computed from.
func TestNodeStatusOf(t *testing.T) {
	at := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	node := func(id string, done, archived bool) nodestore.Summary {
		x := nodestore.Summary{ID: id}
		if done {
			x.Done = at
		}
		if archived {
			x.Archived = at
		}
		return x
	}
	facts := statusFacts{
		cards:   map[string]bool{"card": true, "card-run": true, "card-done": true, "card-gone": true},
		tasks:   map[string]bool{"run": true, "card-run": true, "gone-run": true},
		steps:   map[string]string{"step-run": stepRunning, "step-ok": stepAccepted, "step-drop": stepDropped, "step-failed": stepFailed, "result": stepAccepted},
		results: map[string]bool{"result": true, "work-alone": true},
		settled: map[string]bool{"step-ok": true, "step-drop": true, "work": true},
	}
	for _, tc := range []struct {
		name           string
		id             string
		done, archived bool
		want           string
	}{
		{"nothing", "plain", false, false, nodeIdle},
		{"a card waits on it", "card", false, false, nodeWaits},
		{"a card waits on it while it runs", "card-run", false, false, nodeWaits},
		{"a card waits on a node said done", "card-done", true, false, nodeWaits},
		{"a task works in it", "run", false, false, nodeRunning},
		{"a task held for its start: its card waits, nothing else", "held", false, false, nodeIdle},
		{"a task that ended (failed, stopped, lost) or whose turn ended", "ended", false, false, nodeIdle},
		{"its file says done", "ended", true, false, nodeDone},
		{"a step whose task runs", "step-run", false, false, nodeRunning},
		{"a step accepted", "step-ok", false, false, nodeDone},
		{"a step dropped", "step-drop", false, false, nodeDone},
		{"a step whose proof failed, for the brain", "step-failed", false, false, nodeIdle},
		{"a final result the user has not said done", "result", false, false, nodeWaits},
		{"a final result said done", "result", true, false, nodeDone},
		{"a work done, its results in its steps", "work", false, false, nodeDone},
		{"a work done with no result step: itself", "work-alone", false, false, nodeWaits},
		{"a standing node between its runs", "standing", false, false, nodeIdle},
		{"archived: waits for no one", "card-gone", false, true, nodeIdle},
		{"archived, said done", "card-gone", true, true, nodeDone},
		{"archived, a task still runs in it", "gone-run", false, true, nodeRunning},
	} {
		if got := facts.of(node(tc.id, tc.done, tc.archived)); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
}

// The status comes from what code keeps: a task's state, a held start,
// a card linked and decided, the file's done; a failed task leaves its
// node idle, a standing node too.
func TestNodeStatusFromRecords(t *testing.T) {
	c := graphCore(t)
	c.useNodes(c.nodes)
	id := mustNode(t, c, nodestore.New{Title: "Recherche"})
	if s := nodeStatusNow(t, c, id); s != nodeIdle {
		t.Fatalf("new: %s", s)
	}
	task := workIn(t, c, id, stateRunning)
	if s := nodeStatusNow(t, c, id); s != nodeRunning {
		t.Fatalf("its task runs: %s", s)
	}
	c.store.updateTask(task, map[string]any{"state": stateWaitingPermission})
	if s := nodeStatusNow(t, c, id); s != nodeRunning {
		t.Fatalf("its task waits for a permission, no card asks it: %s", s)
	}
	c.store.updateTask(task, map[string]any{"state": stateFailed})
	if s := nodeStatusNow(t, c, id); s != nodeIdle {
		t.Fatalf("its task failed: %s, not done (the file says done)", s)
	}
	held := workIn(t, c, id, statePending)
	c.store.updateTask(held, map[string]any{"desired": desiredHeld})
	if s := nodeStatusNow(t, c, id); s != nodeIdle {
		t.Fatalf("its task held for its start, no card linked: %s", s)
	}
	c.store.updateTask(held, map[string]any{"state": stateStopped})

	c.store.db.Exec(`INSERT INTO convo_cards (ref, kind, text, created) VALUES ('memory:7', 'memory', 'x', 1)`)
	c.store.db.Exec(`INSERT INTO node_cards (node, ref, created) VALUES (?, 'memory:7', 1)`, id)
	if s := nodeStatusNow(t, c, id); s != nodeWaits {
		t.Fatalf("a card waits: %s", s)
	}
	c.store.db.Exec(`UPDATE convo_cards SET state = ? WHERE ref = 'memory:7'`, cardApproved)
	if s := nodeStatusNow(t, c, id); s != nodeIdle {
		t.Fatalf("its card decided: %s", s)
	}
	c.nodes.SetDone(id, true, byUser)
	if s := nodeStatusNow(t, c, id); s != nodeDone {
		t.Fatalf("said done: %s", s)
	}

	standing := mustNode(t, c, nodestore.New{Title: "Mails"})
	run := workIn(t, c, standing, stateRunning)
	if s := nodeStatusNow(t, c, standing); s != nodeRunning {
		t.Fatalf("a run in a standing node: %s", s)
	}
	c.store.updateTask(run, map[string]any{"state": stateStopped})
	if s := nodeStatusNow(t, c, standing); s != nodeIdle {
		t.Fatalf("a standing node after its run: %s", s)
	}
}

// A card whose proposal went with its task (a held start stopped) stops
// its node waiting as soon as the task moves: the graph's query never
// trusts a card the proposal left behind.
func TestNodeStopsWaitingWithItsTask(t *testing.T) {
	c := graphCore(t)
	c.useNodes(c.nodes)
	id := mustNode(t, c, nodestore.New{Title: "Recherche"})
	task := workIn(t, c, id, statePending)
	c.store.updateTask(task, map[string]any{"desired": desiredHeld})
	res, err := c.store.db.Exec(`INSERT INTO task_starts (task_id, needs, state, created) VALUES (?, '[]', ?, ?)`, task, startProposed, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	start, _ := res.LastInsertId()
	c.cardNow(cardTaskStart, start, "", "task:"+task)
	if s := nodeStatusNow(t, c, id); s != nodeWaits {
		t.Fatalf("held for its card: %s", s)
	}
	c.store.updateTask(task, map[string]any{"state": stateStopped, "desired": desiredStopped})
	c.taskNodeMoved(task, stateStopped)
	if s := nodeStatusNow(t, c, id); s != nodeIdle {
		t.Errorf("its task stopped, its card gone: %s", s)
	}
}

// node_find, node_read and the graph say the same status of a node.
func TestNodeFindAgreesWithGraph(t *testing.T) {
	c := graphCore(t)
	c.useNodes(c.nodes)
	runs := mustNode(t, c, nodestore.New{Title: "Recherche"})
	workIn(t, c, runs, stateRunning)
	waits := mustNode(t, c, nodestore.New{Title: "Devis"})
	workIn(t, c, waits, stateWaitingPermission)
	c.store.db.Exec(`INSERT INTO convo_cards (ref, kind, text, created) VALUES ('access:9', 'access', 'x', 1)`)
	c.store.db.Exec(`INSERT INTO node_cards (node, ref, created) VALUES (?, 'access:9', 1)`, waits)
	done := mustNode(t, c, nodestore.New{Title: "Fini"})
	c.nodes.SetDone(done, true, byUser)
	idle := mustNode(t, c, nodestore.New{Title: "Au repos"})

	rows, err := c.graphRows()
	if err != nil {
		t.Fatal(err)
	}
	graph := map[string]string{}
	for _, r := range rows {
		graph[r.ID] = r.Status
	}
	want := map[string]string{runs: nodeRunning, waits: nodeWaits, done: nodeDone, idle: nodeIdle}
	for id, s := range want {
		if graph[id] != s {
			t.Errorf("graph: %s is %s, want %s", id, graph[id], s)
		}
	}
	out, err := c.brainNodeFind(nodeFindIn{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range out["nodes"].([]nodeFound) {
		if f.Status != graph[f.ID] {
			t.Errorf("node_find: %s is %s, the graph %s", f.ID, f.Status, graph[f.ID])
		}
	}
	for id, s := range want {
		read, err := c.brainNodeRead(brainNodeReadIn{ID: id})
		if err != nil {
			t.Fatal(err)
		}
		if read["status"] != s {
			t.Errorf("node_read: %s is %v, want %s", id, read["status"], s)
		}
	}
}

// graphRows reads the graph in a constant number of queries of core.db
// (and of the index, Graph and Sources once each): as many for
// a graph of 40 nodes, cards, tasks, steps and live sources as for one of
// 4, the first time as later (its caches filled at once).
func TestGraphRowsQueriesConstant(t *testing.T) {
	count := func(n int) int64 {
		c := graphCore(t)
		c.useNodes(c.nodes)
		for i := range n {
			id := mustNode(t, c, nodestore.New{Title: fmt.Sprintf("Nœud %d", i), Body: planBlock})
			readNodeValue(t, c, id, "titles", "ledger", map[string]any{"accounts": []any{}})
			workIn(t, c, id, stateRunning)
			ref := fmt.Sprintf("memory:%d", i+1)
			c.store.db.Exec(`INSERT INTO convo_cards (ref, kind, text, created) VALUES (?, 'memory', 'x', 1)`, ref)
			c.store.db.Exec(`INSERT INTO node_cards (node, ref, created) VALUES (?, ?, 1)`, id, ref)
		}
		c.glances = sync.Map{} // cold, as at start
		counted := countQueries(t, c)
		if _, err := c.graphRows(); err != nil {
			t.Fatal(err)
		}
		return counted.Load()
	}
	small, big := count(4), count(40)
	if small != big || small == 0 {
		t.Errorf("graphRows: %d queries for 4 nodes, %d for 40", small, big)
	}
}

// countQueries counts what c's core.db is asked from now on.
func countQueries(t *testing.T, c *core) *atomic.Int64 {
	t.Helper()
	n := new(atomic.Int64)
	inner := c.store.db.Driver()
	name := fmt.Sprintf("counted-%s-%d", strings.ReplaceAll(t.Name(), "/", "-"), time.Now().UnixNano())
	sql.Register(name, countingDriver{inner, n})
	var path string
	if err := c.store.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open(name, path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	old := c.store.db
	c.store.db = db
	t.Cleanup(func() { c.store.db = old; db.Close() })
	return n
}

type countingDriver struct {
	driver.Driver
	n *atomic.Int64
}

func (d countingDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.Driver.Open(name)
	if err != nil {
		return nil, err
	}
	return countingConn{conn, d.n}, nil
}

type countingConn struct {
	driver.Conn
	n *atomic.Int64
}

func (c countingConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	c.n.Add(1)
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, q, args)
}

func (c countingConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	c.n.Add(1)
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, q, args)
}

func (c countingConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	c.n.Add(1)
	return c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, q)
}
