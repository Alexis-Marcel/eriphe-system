package main

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// reviewWorld is a core whose monitor server changed since it was added:
// status's definition changed, alerts is new, and a tool it no longer
// lists (gone) was pinned. Its « Outils à revoir », read once.
func reviewWorld(t *testing.T) (*testWeb, string, string) {
	t.Helper()
	c, _, _ := viewCore(t)
	u := c.pool.get("monitor")
	waitStatus(t, c.pool.get(reviewServer), statusReady)
	if err := c.store.pinTool("monitor", &mcp.Tool{Name: "status", Description: "The site's status, as it was.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"site": map[string]any{"type": "string"}}}}); err != nil {
		t.Fatal(err)
	}
	if err := c.store.unpinTool("monitor", "alerts"); err != nil {
		t.Fatal(err)
	}
	if err := c.store.pinTool("monitor", &mcp.Tool{Name: "gone", Description: "A tool no longer there."}); err != nil {
		t.Fatal(err)
	}
	u.refreshTools()
	if held := u.heldTools(); !slices.Equal(held, []string{"alerts", "status"}) && !slices.Equal(held, []string{"status", "alerts"}) {
		t.Fatalf("held: %v", held)
	}
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	id, err := c.ensureToolReview()
	if err != nil {
		t.Fatal(err)
	}
	c.live.wg.Wait()
	c.readNode(context.Background(), id, nil, time.Now())
	return tw, csrf, id
}

// reviewIDs are the ids of the changes the node's source lists now, by
// tool.
func reviewIDs(t *testing.T, c *core, id string) map[string]string {
	t.Helper()
	r, ok := c.nodeValue(id, reviewSource)
	if !ok {
		t.Fatal("the review source has no value")
	}
	raw, _ := json.Marshal(r.Value)
	var v struct {
		Changes []toolChange `json:"changes"`
	}
	json.Unmarshal(raw, &v)
	out := map[string]string{}
	for _, ch := range v.Changes {
		out[ch.Tool] = ch.ID
	}
	return out
}

// Each change waits with its id, its kind, what it said before and says
// now, a card on its row; the node made once waits for the user while
// some do, because their cards wait.
func TestToolChangesListed(t *testing.T) {
	tw, _, id := reviewWorld(t)
	c := tw.c
	chs := c.toolChanges("fr")
	if len(chs) != 3 {
		t.Fatalf("changes: %+v", chs)
	}
	by := map[string]toolChange{}
	for _, ch := range chs {
		by[ch.Tool] = ch
	}
	if s := by["status"]; s.Kind != changeChanged || s.OldDesc != "The site's status, as it was." || s.OldSchema != "envoyer · site" || s.KindText != "changé" ||
		!strings.HasPrefix(s.ID, "monitor/status@") || s.Label != "monitor · status" {
		t.Errorf("status: %+v", s)
	}
	if a := by["alerts"]; a.Kind != changeNew || a.OldDesc != "" || a.NewSchema == "" {
		t.Errorf("alerts: %+v", a)
	}
	if g := by["gone"]; g.Kind != changeRemoved || g.OldDesc != "A tool no longer there." || !strings.Contains(g.ID, "@-") {
		t.Errorf("gone: %+v", g)
	}

	// The node: its goal and title code's, its block external (the
	// servers' words), made once.
	sum, err := c.nodes.Get(id)
	if d, _ := c.nodes.Read(id, 0); err != nil || d.Node.Meta.Goal != reviewGoal || sum.Title != "Outils à revoir" || sum.TitleExternal() {
		t.Fatalf("node: %+v %v", sum, err)
	}
	if b := block(t, c, id, "tools"); !b.External {
		t.Errorf("block: external %v", b.External)
	}
	if again, _ := c.ensureToolReview(); again != id {
		t.Errorf("made again: %s, was %s", again, id)
	}
	ids := reviewIDs(t, c, id)
	if len(ids) != 3 {
		t.Errorf("the source lists %v", ids)
	}
	cards := c.itemCards(id, reviewSource)
	for tool, ch := range ids {
		if !strings.HasPrefix(cards[ch], cardCalls+":") {
			t.Errorf("%s has no card on its row: %v", tool, cards)
		}
	}
	if c.graphRow(sum).Status != nodeWaits {
		t.Error("the node does not wait while its cards do")
	}
	// Read again: no card twice.
	c.readNode(context.Background(), id, nil, time.Now())
	var n int
	c.store.db.QueryRow(`SELECT COUNT(*) FROM access_asks WHERE server = ?`, reviewServer).Scan(&n)
	if n != 3 {
		t.Errorf("%d cards for 3 changes", n)
	}
	// Archived, it is made again: it is the one place the changes wait.
	if err := c.nodes.Archive(id, byUser); err != nil {
		t.Fatal(err)
	}
	if again, _ := c.ensureToolReview(); again == id || again == "" {
		t.Errorf("not made again after its archive: %q", again)
	}
}

// reviewCard is the card on a change's row, decided: yes or no.
func decideReviewCard(t *testing.T, c *core, node, change string, yes bool) {
	t.Helper()
	ref := c.itemCards(node, reviewSource)[change]
	xs, err := c.cardsWhere(`ref = ?`, ref)
	if err != nil || len(xs) != 1 {
		t.Fatalf("card %q of %s: %v %v", ref, change, xs, err)
	}
	if _, err := c.decideCard(context.Background(), xs[0].ID, yes, "web"); err != nil {
		t.Fatal(err)
	}
	c.yeses.Wait()
}

// A change's card: its Oui accepts it (the tool is lent with that
// definition), its ✕ refuses it (it stays held, and no longer waits).
// Each yes is on record as the user's, on the card; once nothing is left,
// the node rests.
func TestToolReviewCards(t *testing.T) {
	tw, _, id := reviewWorld(t)
	c := tw.c
	u := c.pool.get("monitor")
	ids := reviewIDs(t, c, id)
	decideReviewCard(t, c, id, ids["status"], true)
	if slices.Contains(u.heldTools(), "status") || u.tool("status") == nil {
		t.Errorf("status still held: %v", u.heldTools())
	}
	rows, _ := c.store.decisions(0, 5)
	if len(rows) == 0 || rows[0].By != byUser || rows[0].Tool != "mcp__eriphe-catalog__catalog_accept" || !strings.Contains(rows[0].Reason, "carte") {
		t.Fatalf("journal: %+v", rows)
	}
	c.live.wg.Wait()
	decideReviewCard(t, c, id, ids["alerts"], false)
	decideReviewCard(t, c, id, ids["gone"], false)
	c.live.wg.Wait()
	c.readNode(context.Background(), id, nil, time.Now()) // its list read again: the ✕ said refuses
	if !slices.Contains(u.heldTools(), "alerts") {
		t.Error("a refused tool is lent")
	}
	if pins, _ := c.store.toolPins("monitor"); pins["gone"] == "" {
		t.Error("a refused drop forgot its pin")
	}
	if left := c.toolChanges("fr"); len(left) != 0 {
		t.Errorf("left to review: %+v", left)
	}
	sum, _ := c.nodes.Get(id)
	if c.graphRow(sum).Status == nodeWaits {
		t.Error("the node waits with nothing to review")
	}
	// A change no longer waiting does nothing.
	if n, err := c.settleChanges([]string{ids["status"]}, true); err == nil || n != 0 {
		t.Errorf("an id settled twice: %d %v", n, err)
	}
	// The server's page still accepts the refused ones at once.
	if err := c.approveTools("monitor"); err != nil {
		t.Fatal(err)
	}
	if len(u.heldTools()) != 0 {
		t.Errorf("held after the page's yes: %v", u.heldTools())
	}
	// Through it all, nothing was said to the brain (it is configured).
	if c.conf().brain() == nil {
		t.Fatal("no brain: the check below proves nothing")
	}
	msgs, _ := c.store.messagesAfter(0, 50)
	for _, m := range msgs {
		if m.Role != "eriphe" && (strings.Contains(m.Text, "monitor") || strings.Contains(m.Text, "outil")) {
			t.Errorf("the brain is told: %+v", m)
		}
	}
}

// A change accepted elsewhere (its server's page in Réglages) waits no
// more: its card is withdrawn at the next read of the list, gone, never
// read as the user's no.
func TestToolReviewCardWithdrawn(t *testing.T) {
	tw, _, id := reviewWorld(t)
	c := tw.c
	ids := reviewIDs(t, c, id)
	ref := c.itemCards(id, reviewSource)[ids["status"]]
	if err := c.approveTools("monitor"); err != nil {
		t.Fatal(err)
	}
	c.live.wg.Wait()
	c.readNode(context.Background(), id, nil, time.Now())
	xs, _ := c.cardsWhere(`ref = ?`, ref)
	if len(xs) != 1 || xs[0].State != cardGone {
		t.Fatalf("its card: %+v", xs)
	}
	if refused := c.store.refusedChanges(); len(refused) != 0 {
		t.Errorf("a withdrawn card refused %v", refused)
	}
}

// Only the user's yes on a card code made accepts: the brain and a task,
// even with a yes given before, are refused, and no card is kept for
// them; the changes are read by code alone, lent to no agent.
func TestToolReviewNeverForAgents(t *testing.T) {
	tw, _, _ := reviewWorld(t)
	c := tw.c
	ctx := context.Background()
	review := c.pool.get(reviewServer)
	for _, tool := range []string{"catalog_accept"} {
		name := claudeToolName(reviewServer, tool)
		input := map[string]any{"ids": []any{"monitor/status@x"}}
		if d, _ := c.decideBrainCall(name, input, nil); d != decideDeny {
			t.Errorf("%s: the brain gets %s", tool, d)
		}
		c.store.allowAction(reviewServer, name, "") // « toujours autoriser », somehow
		if d, _ := c.decideBrainCall(name, input, nil); d != decideDeny {
			t.Errorf("%s: the brain gets %s with a rule", tool, d)
		}
		c.store.insertTask(&TaskRow{ID: "t-" + tool, Kind: "task", Device: "pc", Runtime: "claude-code"})
		if d, _ := c.decideTask(&TaskRow{ID: "t-" + tool}, name, input); d != decideDeny {
			t.Errorf("%s: a task gets %s", tool, d)
		}
		for _, task := range []string{"t-brain", "t-" + tool} {
			c.store.addTicket(task, name, input)
			if res := c.proxyCall(ctx, task, []string{reviewServer}, review, tool, json.RawMessage(`{"ids":["monitor/status@x"]}`)); !res.IsError {
				t.Errorf("%s: %s's call ran", tool, task)
			}
		}
		if slices.ContainsFunc(c.lentTools(), func(x catalogTool) bool { return x.server == reviewServer && x.tool == tool }) {
			t.Errorf("%s is lent", tool)
		}
	}
	if len(c.toolChanges("fr")) != 3 {
		t.Error("an agent's call settled something")
	}
	var kept int
	c.store.db.QueryRow(`SELECT COUNT(*) FROM access_asks WHERE server = ?`, reviewServer).Scan(&kept)
	if kept != 3 {
		t.Errorf("%d calls kept: an agent's attempt made a card", kept)
	}
	if review.toolByClaudeName("catalog_refuse") != nil {
		t.Error("catalog_refuse is a tool: a no is the card's ✕")
	}
	// No agent is lent the server, nor may a grant name it: the changes
	// are code's own read, never the brain's.
	if slices.ContainsFunc(c.brainGrant(), func(g string) bool { return strings.HasPrefix(g, reviewServer) }) || slices.Contains(c.catalogNames(), reviewServer) {
		t.Errorf("lent: grant %v", c.brainGrant())
	}
	if _, err := c.conf().checkGrant([]string{reviewServer}); err == nil {
		t.Error("a grant names it")
	}
	if why := c.blockSourceWhy(monitorSpec{Way: wayPoll, Server: reviewServer, Tool: "catalog_changes"}); why != "" {
		t.Errorf("not a source: %s", why)
	}
	if why := c.blockSourceWhy(monitorSpec{Way: wayPoll, Server: reviewServer, Tool: "catalog_accept"}); why == "" {
		t.Error("catalog_accept is a source")
	}
	// Code's read only: no monitor looks at it, no author writes it.
	if why := c.sourceWhy(monitorSpec{Way: wayPoll, Server: reviewServer, Tool: "catalog_changes"}); why == "" {
		t.Error("a monitor may look at the changes")
	}
	id := mustNode(t, c, nodestore.New{Title: "Ailleurs", Body: "x\n"})
	_, err := c.authorWrite(id, lastHash(t, c, id), byBrain, false, c.nodeSourceOK(), func(n *nodefile.Node) error {
		return n.AuthorBody("x\n\n" + listBlock("b", "ch", map[string]any{"tool": reviewServer + "/catalog_changes", "items": map[string]any{"path": "changes", "id": "id"}}))
	})
	if err == nil {
		t.Error("the brain writes a block reading the changes")
	}
}
