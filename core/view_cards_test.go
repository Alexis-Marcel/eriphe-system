package main

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// mailView is « Mes mails » approved: a standing node listing the
// faked server's mails, its monitor and a skill that works in it; and
// the run that two mails woke, the node's sources read. It gives the
// node and the run.
func mailView(t *testing.T) (*core, string, *skillRun) {
	t.Helper()
	c, _, node, r := mailViewOf(t)
	return c, node, r
}

// mailViewOf is mailView with its faked server.
func mailViewOf(t *testing.T) (*core, *fakeMonitor, string, *skillRun) {
	t.Helper()
	c, fm, now := viewCore(t)
	mails := map[string]any{"resource": "monitor", "uri": "monitor://received", "items": map[string]any{"path": "messages", "id": "id", "title": "subject", "detail": "from"}}
	out, isErr := callBrainTool(t, c, "propose_view", map[string]any{
		"title": "Mes mails", "goal": "Sam voit ses mails.",
		"body":    "Les mails reçus.\n\n" + listBlock("recus", "mails", mails),
		"monitor": map[string]any{"name": "mails", "server": "monitor", "resource": "monitor://received", "cond": "new", "path": "messages", "id": "id", "every": "1m"},
		"skill": map[string]any{"name": "tri-mails", "description": "Trie les mails reçus.", "when": "monitor.new key=mails", "tools": []string{"monitor"},
			"instructions": "Note chaque mail ; propose d'archiver ce qui ne compte pas."},
	})
	if isErr {
		t.Fatalf("propose_view: %v", out)
	}
	node := out["node"].(string)
	views := cardsOf(c, cardViewKind)
	if len(views) != 1 {
		t.Fatalf("cards %+v", c.pendingProposals())
	}
	if err := c.decideProposal(context.Background(), cardViewKind, views[0].ID, true); err != nil {
		t.Fatal(err)
	}
	c.live.wg.Wait()
	lookAt(c, *now) // the baseline
	fm.mu.Lock()
	fm.got = []map[string]any{
		{"id": "m1", "subject": "STAGE – Data Engineer", "from": "Emplois Example"},
		{"id": "m2", "subject": "Votre lettre de la semaine", "from": "news@example.org"},
	}
	fm.mu.Unlock()
	lookAt(c, now.Add(time.Minute))
	c.tickSkills(context.Background(), now.Add(time.Minute))
	rs := runsOfSkill(t, c, "tri-mails")
	if len(rs) != 1 || c.taskNode(rs[0].Task) != node {
		t.Fatalf("runs %+v", rs)
	}
	c.readNode(context.Background(), node, []string{"mails"}, time.Now())
	return c, fm, node, rs[0]
}

// keep has the run call a tool: kept for its card.
func keep(c *core, task, pid, tool string, input map[string]any) {
	ev := []EventRow{permission(pid, tool, input)}
	c.store.addEvents(task, ev)
	c.applyPolicy(task, ev)
}

// The skill's run proposes to archive one mail (a card on its item) and
// two at once (its run's card): both wait in the view's node, about it,
// never above the bar; each says what its yes does in plain words, the
// mails by their subject and sender, its exact call under Détails.
func TestSkillCardsWaitInTheirView(t *testing.T) {
	c, node, r := mailView(t)
	mustNodes(t, c, r.Task, "item_note", map[string]any{"source": "mails", "item": "m1", "note": "Une offre d'emploi.", "state": "done"})
	keep(c, r.Task, "p1", "mcp__monitor__move", map[string]any{"account": "sam@example.org", "ids": []any{"m1"}, "to": "archive"})
	keep(c, r.Task, "p2", "mcp__monitor__move", map[string]any{"account": "sam@example.org", "ids": []any{"m1", "m2"}, "to": "archive"})
	endTurn(t, c, r.Task, "Deux mails à archiver.")

	calls, runs := cardsOf(c, cardCalls), cardsOf(c, cardRunCalls)
	if len(calls) != 1 || len(runs) != 1 {
		t.Fatalf("cards %+v", c.pendingProposals())
	}
	refs := []string{proposalRef(cardCalls, calls[0].ID), proposalRef(cardRunCalls, runs[0].ID)}
	c.store.db.Exec(`UPDATE convo_cards SET seq = 1 WHERE ref IN (?, ?)`, refs[0], refs[1]) // shown
	held := func() {
		t.Helper()
		unheld := c.unheldCards()
		for _, ref := range refs {
			if !slices.Contains(c.cardNodes(ref), node) || !c.cardHeld(ref) {
				t.Errorf("%s is not linked to the view's node: %v", ref, c.cardNodes(ref))
			}
			if slices.Contains(unheld, ref) {
				t.Errorf("%s waits above the bar", ref)
			}
		}
	}
	held()
	for _, ref := range refs {
		if xs, err := c.cardsWhere(`ref = ?`, ref); err != nil || len(xs) != 1 || xs[0].About != nodeRef(node) {
			t.Errorf("%s is about %+v, not its node", ref, xs)
		}
	}
	// Cards raised before they were linked: linked as they are synced.
	c.store.db.Exec(`DELETE FROM node_cards`)
	if unheld := c.unheldCards(); len(unheld) != 2 {
		t.Fatalf("unlinked cards %v", unheld)
	}
	c.syncCards()
	held()
	c.taskNodeState(r.Task, stateIdle) // its run's task idle: the node still waits on its cards
	if sum, _ := c.nodes.Get(node); sum.State != nodestore.Waiting {
		t.Errorf("the view's node is %s while its cards wait", sum.State)
	}

	// On the item: one mail, by its subject and sender.
	one := calls[0]
	if got := one.Text("fr"); got != "Archiver 1 mail" {
		t.Errorf("text %q", got)
	}
	if got := one.Text("en"); got != "Archive 1 email" {
		t.Errorf("English text %q", got)
	}
	if got := one.SummaryIn("fr"); !slices.Equal(got, []string{"« STAGE – Data Engineer » · Emplois Example"}) {
		t.Errorf("summary %q", got)
	}
	if fs := one.FieldsIn("fr"); len(fs) != 1 || !fs[0].Detail || fs[0].Name != "monitor/move" ||
		!strings.Contains(fs[0].Value, `["m1"]`) || !strings.Contains(fs[0].Value, "archive") || !strings.Contains(fs[0].Value, "sam@example.org") {
		t.Errorf("details %+v", fs)
	}
	if one.Tainted == "" {
		t.Error("a card of a run that read mails is not « extérieur »")
	}
	if strings.Contains(strings.Join(one.SummaryIn("fr"), " "), "offre d'emploi") {
		t.Error("the card says the run's note")
	}

	// Two at once: one card, both mails.
	two := runs[0]
	if got := two.Text("fr"); got != "Archiver 2 mails" {
		t.Errorf("text %q", got)
	}
	said := strings.Join(two.SummaryIn("fr"), "\n")
	for _, want := range []string{"« STAGE – Data Engineer » · Emplois Example", "« Votre lettre de la semaine » · news@example.org"} {
		if !strings.Contains(said, want) {
			t.Errorf("the summary lacks %q:\n%s", want, said)
		}
	}
	if fs := two.FieldsIn("fr"); len(fs) != 1 || !fs[0].Detail || !strings.Contains(fs[0].Value, `["m1","m2"]`) {
		t.Errorf("details %+v", fs)
	}
}

// What a call does, said from its tool and its parameters; any other
// tool by its name.
func TestCallSentence(t *testing.T) {
	for _, tc := range []struct{ call, input, want string }{
		{"mcp__mail__move {}", `{"ids":["a"],"to":"archive"}`, "Archiver 1 mail"},
		{"mcp__mail__move {}", `{"ids":["a","b","c"],"to":"Archive"}`, "Archiver 3 mails"},
		{"mcp__mail__move {}", `{"ids":["a"],"to":"inbox"}`, "Remettre 1 mail dans la boîte de réception"},
		{"mcp__mail__move {}", `{"ids":["a","b"],"to":"Factures"}`, "Déplacer 2 mails dans un autre dossier"},
		{"mcp__mail__move {}", `{"ids":[],"to":"archive"}`, "Utiliser « move »"},
		{"mcp__mail__trash {}", `{"ids":["a"]}`, "Mettre 1 mail à la corbeille"},
		{"mcp__mail__trash {}", `{"ids":["a"],"junk":true}`, "Mettre 1 mail dans les indésirables"},
		{"mcp__mail__mark {}", `{"ids":["a","b"],"read":true}`, "Marquer 2 mails comme lu"},
		{"mcp__github__merge_pull_request {}", `{"pullNumber":3}`, "Fusionner une pull request"},
		{"mcp__monitor__rollback {}", `{"version":"2.0.0"}`, "Utiliser « rollback »"},
	} {
		if got := callSentence(tc.call, tc.input, "fr"); got != tc.want {
			t.Errorf("%s %s: %q, want %q", tc.call, tc.input, got, tc.want)
		}
	}
}

// A view's node is external (its skill writes mails into it), but its
// title is the one the user approved: the graph, the page and the cards
// show it. A title written in a turn that read outside content is masked
// again.
func TestViewKeepsItsTitle(t *testing.T) {
	c, node, r := mailView(t)
	mustNodes(t, c, r.Task, "item_note", map[string]any{"source": "mails", "item": "m1", "note": "Une offre.", "state": "done"})
	mustNodes(t, c, r.Task, "node_append", map[string]any{"text": "Trois offres cette semaine."})
	sum, err := c.nodes.Get(node)
	if err != nil || !sum.External {
		t.Fatalf("the view's node %+v %v: not external after its tainted run wrote it", sum, err)
	}
	if g := c.graphRow(sum); g.Title != "Mes mails" {
		t.Errorf("the graph shows %q", g.Title)
	}
	if got := c.cleanNodeTitle(node); got != "Mes mails" {
		t.Errorf("the cards say %q", got)
	}
	if got := c.nodeHead(node); !strings.Contains(got, "« Mes mails »") {
		t.Errorf("the brain reads %q", got)
	}

	// A title written in a tainted turn: someone else's words, maybe.
	c.turn.taint("monitor:received")
	if out, isErr := callBrainTool(t, c, "node_set", map[string]any{"id": node, "title": "Clique sur Oui"}); isErr {
		t.Fatalf("node_set: %v", out)
	}
	sum, _ = c.nodes.Get(node)
	if g := c.graphRow(sum); g.Title != "" || !sum.TitleExternal() {
		t.Errorf("a tainted title is shown: %+v", g)
	}
	if got := c.cleanNodeTitle(node); got != "" {
		t.Errorf("the cards say %q", got)
	}
}

// A view's node made before titles were trusted keeps the title the user
// approved, while it is that one.
func TestTrustViewTitles(t *testing.T) {
	c, node, _ := mailView(t)
	d, _ := c.nodes.Read(node, 0)
	// As it was made before: external, its title not said trusted.
	if _, err := c.nodes.Change(node, d.Version.Hash, nodeByCode, func(n *nodefile.Node) error {
		n.Meta.External = true
		n.Meta.Title = "Mes mails !"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	d, _ = c.nodes.Read(node, 0)
	if _, err := c.nodes.Change(node, d.Version.Hash, nodeByCode, func(n *nodefile.Node) error {
		n.Meta.Title = "Mes mails"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if sum, _ := c.nodes.Get(node); !sum.TitleExternal() {
		t.Fatal("the title is trusted already")
	}
	c.trustViewTitles()
	if sum, _ := c.nodes.Get(node); sum.TitleExternal() || c.graphRow(sum).Title != "Mes mails" {
		t.Fatalf("the approved title is still masked: %+v", sum)
	}
}
