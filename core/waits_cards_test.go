package main

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// What waits for the user is a card (#726): a node waits when a card
// waiting is linked to it (or a final result waits for their word), never
// because a task is held or waits for a permission without one.

// A release card withdrawn with its node (archived) refuses the calls that
// waited on it: the task never hangs with nothing on screen.
func TestWithdrawnReleaseRefusesItsCalls(t *testing.T) {
	c := newMailCore(t, accessFree, func(c *core) { openGraph(t, c); c.useNodes(c.nodes) }) // before its pool runs
	node := mustNode(t, c, nodestore.New{Title: "Tri des mails"})
	heldTask(t, c, "t-1")
	c.store.updateTask("t-1", map[string]any{"state": stateWaitingPermission})
	c.store.db.Exec(`INSERT INTO task_nodes (task, node) VALUES ('t-1', ?)`, node)
	if got := request(t, c, "t-1", "p-1", "move"); got != "user" {
		t.Fatalf("a held tool: %s", got)
	}
	if err := c.nodes.Archive(node, byUser); err != nil {
		t.Fatal(err)
	}
	c.withdrawCards(context.Background(), []string{node}) // as the archive does
	if accessCards(t, c) != 0 {
		t.Fatal("the release card still waits")
	}
	items, _ := c.store.inbox("t-1")
	if !slices.ContainsFunc(items, func(it InboxItem) bool { return it.Data["id"] == "p-1" && it.Data["allow"] == false }) {
		t.Errorf("the call that waited is not refused: %+v", items)
	}
	if c.accessAsked("t-1", "p-1") {
		t.Error("the call still waits on a card")
	}
}

// nodeToldSince tells whether the graph was told node moved after seq.
func nodeToldSince(c *core, seq int64, node string) bool {
	evs, _, _, _ := c.graphAfter(seq)
	return slices.ContainsFunc(evs, func(ev graphEvent) bool { return ev.Kind == graphEvNode && ev.Node == node })
}

// A node waits iff a waiting card is on it, or a final result waits for
// the user's word: a task held, or waiting for a permission, with no card
// linked, leaves it at work or idle.
func TestNodeWaitsIffACard(t *testing.T) {
	for _, tc := range []struct {
		name   string
		facts  statusFacts
		done   bool
		waits  bool
		status string
	}{
		{"nothing", statusFacts{}, false, false, nodeIdle},
		{"a waiting card", statusFacts{cards: map[string]bool{"n": true}}, false, true, nodeWaits},
		{"a waiting card, a task at work", statusFacts{cards: map[string]bool{"n": true}, tasks: map[string]bool{"n": true}}, false, true, nodeWaits},
		{"a waiting card on a node said done", statusFacts{cards: map[string]bool{"n": true}}, true, true, nodeWaits},
		{"a final result not said done", statusFacts{results: map[string]bool{"n": true}}, false, true, nodeWaits},
		{"a final result said done", statusFacts{results: map[string]bool{"n": true}}, true, false, nodeDone},
		{"a task at work, a permission without a card", statusFacts{tasks: map[string]bool{"n": true}}, false, false, nodeRunning},
	} {
		sum := nodestore.Summary{ID: "n"}
		if tc.done {
			sum.Done = time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
		}
		got := tc.facts.of(sum)
		if (got == nodeWaits) != tc.waits || got != tc.status {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.status)
		}
	}
}

// A task that calls a tool lent held back waits on a card: its release,
// raised by code at once, linked to the task's node, which waits. Its no
// refuses the call and the node waits no more; asked again, a yes
// releases it.
func TestHeldCallWaitsOnItsCard(t *testing.T) {
	c := newMailCore(t, accessFree, func(c *core) { openGraph(t, c); c.useNodes(c.nodes) }) // before its pool runs
	node := mustNode(t, c, nodestore.New{Title: "Tri des mails"})
	heldTask(t, c, "t-1")
	c.store.updateTask("t-1", map[string]any{"state": stateWaitingPermission})
	c.store.db.Exec(`INSERT INTO task_nodes (task, node) VALUES ('t-1', ?)`, node)

	if got := request(t, c, "t-1", "p-1", "move"); got != "user" {
		t.Fatalf("a held tool: %s, want the user's", got)
	}
	x := accessCard(t, c, "mail.move")
	if !slices.Contains(c.cardNodes(x.Ref), node) {
		t.Fatalf("its card is not on the task's node: %v", c.cardNodes(x.Ref))
	}
	if !c.accessAsked("t-1", "p-1") {
		t.Error("the request does not wait on its card")
	}
	if s := nodeStatusNow(t, c, node); s != nodeWaits {
		t.Fatalf("the node of a task waiting on a card: %s", s)
	}
	if got := request(t, c, "t-1", "p-2", "move"); got != "user" || accessCards(t, c) != 1 {
		t.Fatalf("the same release asked twice: %s, %d cards", got, accessCards(t, c))
	}
	if _, err := c.decideCard(context.Background(), x.ID, false, "web"); err != nil {
		t.Fatal(err)
	}
	if s := nodeStatusNow(t, c, node); s == nodeWaits {
		t.Errorf("its card answered no, the node still waits")
	}
	items, _ := c.store.inbox("t-1")
	refused := 0
	for _, it := range items {
		if it.Data["allow"] == false && (it.Data["id"] == "p-1" || it.Data["id"] == "p-2") {
			refused++
		}
	}
	if refused != 2 {
		t.Errorf("the calls that waited on the card are not refused: %+v", items)
	}

	if got := request(t, c, "t-1", "p-3", "move"); got != "user" {
		t.Fatalf("asked again: %s", got)
	}
	x = accessCard(t, c, "mail.move")
	if _, err := c.decideCard(context.Background(), x.ID, true, "web"); err != nil {
		t.Fatal(err)
	}
	items, _ = c.store.inbox("t-1")
	if !slices.ContainsFunc(items, func(it InboxItem) bool { return it.Data["id"] == "p-3" && it.Data["allow"] == true }) {
		t.Errorf("released, the call is not allowed: %+v", items)
	}
	if tk, _ := c.store.task("t-1"); tk.Resources[1].Hold {
		t.Error("the release is not kept with the task")
	}
}

// A start the brain held is on the task's node too, not only on the node
// of the turn that launched it: the node of the work waits for its yes.
func TestHeldStartWaitsOnTheTasksNode(t *testing.T) {
	c := graphCore(t)
	c.useNodes(c.nodes)
	node := mustNode(t, c, nodestore.New{Title: "Déploiement"})
	task := workIn(t, c, node, statePending)
	c.store.updateTask(task, map[string]any{"desired": desiredHeld})
	if err := c.holdTask(&TaskRow{ID: task}, []startNeed{{Server: "nas", Category: "use"}}, nil, true); err != nil {
		t.Fatal(err)
	}
	var start int64
	c.store.db.QueryRow(`SELECT id FROM task_starts WHERE task_id = ?`, task).Scan(&start)
	if !slices.Contains(c.cardNodes(proposalRef(cardTaskStart, start)), node) {
		t.Fatalf("the start card is not on the task's node: %v", c.cardNodes(proposalRef(cardTaskStart, start)))
	}
	if s := nodeStatusNow(t, c, node); s != nodeWaits {
		t.Errorf("a task held for its start card: %s", s)
	}
}

// A card answered elsewhere than on it (a page, the user's own change, a
// task stopped) moves at once, and its node with it: no timer, no
// reconcile. Each case changes the proposal only, then reads the card's
// row and the graph's events straight away.
func TestCardAnsweredElsewhereMovesAtOnce(t *testing.T) {
	c, _ := testMemoryCore(t)
	openGraph(t, c)
	c.useNodes(c.nodes)
	for _, tc := range []struct {
		name string
		make func(node string) string // raises a card on node, gives its ref
		move func(ref string)         // answers it elsewhere
		want string
	}{
		{"an intention cancelled on its page", func(node string) string {
			x := mustIntend(t, c.mem, &Intention{Text: "Relancer Sam", State: intentProposed, SourceKind: srcTask, SourceRef: "task:t-1", Trust: trustOwner})
			c.cardNow(cardIntention, x.ID, "", nodeRef(node))
			return proposalRef(cardIntention, x.ID)
		}, func(ref string) {
			_, id, _ := parseProposalRef(ref)
			if _, err := c.mem.changeIntention(id, intentionChange{State: intentCancelled}, "owner", "page"); err != nil {
				t.Fatal(err)
			}
		}, cardRejected},
		{"a rule deleted on its page", func(node string) string {
			r, err := c.addNotifyRule(notifyRule{Kind: ruleTopic, Match: "banque", Ceiling: 2, State: ruleProposed, By: "brain"})
			if err != nil {
				t.Fatal(err)
			}
			c.cardNow(cardRule, r.ID, "", nodeRef(node))
			return proposalRef(cardRule, r.ID)
		}, func(ref string) {
			_, id, _ := parseProposalRef(ref)
			if err := c.deleteNotifyRule(id); err != nil {
				t.Fatal(err)
			}
		}, cardGone},
		{"a setting proposed again", func(node string) string {
			id, err := c.proposeSetting(settingProposal{Name: setCap, Value: "3", Why: "Trop de notifications."})
			if err != nil {
				t.Fatal(err)
			}
			c.cardNow(cardSetting, id, "", nodeRef(node))
			return proposalRef(cardSetting, id)
		}, func(string) {
			if _, err := c.proposeSetting(settingProposal{Name: setCap, Value: "4", Why: "Plutôt 4."}); err != nil {
				t.Fatal(err)
			}
		}, cardGone},
		{"a monitor proposed, deleted by the user", func(node string) string {
			res, err := c.store.db.Exec(`INSERT INTO monitors (name, state, created, updated) VALUES ('disque', ?, 1, 1)`, monitorProposed)
			if err != nil {
				t.Fatal(err)
			}
			id, _ := res.LastInsertId()
			c.cardNow(cardMonitor, id, "", nodeRef(node))
			return proposalRef(cardMonitor, id)
		}, func(string) {
			if err := c.deleteMonitor("disque"); err != nil {
				t.Fatal(err)
			}
		}, cardGone},
		{"a skill the brain wrote, deleted in Réglages", func(node string) string {
			k, _, err := c.store.addSkillVersion(skillFiles(skillMD), skillFromEriphe)
			if err != nil {
				t.Fatal(err)
			}
			var id int64
			c.store.db.QueryRow(`SELECT rowid FROM skills WHERE name = ?`, k.Name).Scan(&id)
			c.cardNow(cardSkill, id, "", nodeRef(node))
			return proposalRef(cardSkill, id)
		}, func(ref string) {
			_, id, _ := parseProposalRef(ref)
			var name string
			c.store.db.QueryRow(`SELECT name FROM skills WHERE rowid = ?`, id).Scan(&name)
			if err := c.deleteSkill(name); err != nil {
				t.Fatal(err)
			}
		}, cardGone},
		{"a held start whose task is stopped", func(node string) string {
			task := workIn(t, c, node, statePending)
			c.store.updateTask(task, map[string]any{"desired": desiredHeld})
			if err := c.holdTask(&TaskRow{ID: task}, []startNeed{{Server: "nas", Category: "use"}}, nil, false); err != nil {
				t.Fatal(err)
			}
			var id int64
			c.store.db.QueryRow(`SELECT id FROM task_starts WHERE task_id = ?`, task).Scan(&id)
			return proposalRef(cardTaskStart, id)
		}, func(ref string) {
			_, id, _ := parseProposalRef(ref)
			s, _ := c.taskStart(id)
			if err := c.stop(context.Background(), s.TaskID); err != nil {
				t.Fatal(err)
			}
		}, cardGone},
	} {
		node := mustNode(t, c, nodestore.New{Title: tc.name})
		ref := tc.make(node)
		if s := nodeStatusNow(t, c, node); s != nodeWaits {
			t.Fatalf("%s: before, the node is %s", tc.name, s)
		}
		seq, _ := c.graphHead()
		tc.move(ref)
		xs, err := c.cardsWhere(`ref = ?`, ref)
		if err != nil || len(xs) != 1 || xs[0].State != tc.want {
			t.Errorf("%s: its card %+v, want %s", tc.name, xs, tc.want)
		}
		if !nodeToldSince(c, seq, node) {
			t.Errorf("%s: the graph was not told its node moved", tc.name)
		}
		if s := nodeStatusNow(t, c, node); s == nodeWaits {
			t.Errorf("%s: its node still waits", tc.name)
		}
	}
}
