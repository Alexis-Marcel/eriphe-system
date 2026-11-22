package main

import (
	"context"
	"encoding/json"
	"net/http"
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
// now; the node made once waits for the user while some do.
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

	// The node: standing, its title code's, its block external (the
	// servers' words), made once.
	sum, err := c.nodes.Get(id)
	if err != nil || sum.Lifetime != nodefile.Standing || sum.Title != "Outils à revoir" || sum.TitleExternal() {
		t.Fatalf("node: %+v %v", sum, err)
	}
	if b := block(t, c, id, "tools"); !b.External || !b.Sources[reviewSource].Attention {
		t.Errorf("block: external %v, source %+v", b.External, b.Sources[reviewSource])
	}
	if again, _ := c.ensureToolReview(); again != id {
		t.Errorf("made again: %s, was %s", again, id)
	}
	if ids := reviewIDs(t, c, id); len(ids) != 3 {
		t.Errorf("the source lists %v", ids)
	}
	if c.graphRow(sum).Status != nodeWaits {
		t.Error("the node does not wait while changes do")
	}
	// Archived, it is made again: it is the one place the changes wait.
	if err := c.nodes.Archive(id, byUser); err != nil {
		t.Fatal(err)
	}
	if again, _ := c.ensureToolReview(); again == id || again == "" {
		t.Errorf("not made again after its archive: %q", again)
	}
}

// The user's click accepts or refuses the changes picked: accepted, the
// tool is lent with that definition; refused, it stays held and no longer
// waits. Each click is on record as the user's own gesture; once nothing
// is left, the node rests.
func TestToolReviewActs(t *testing.T) {
	tw, csrf, id := reviewWorld(t)
	c := tw.c
	u := c.pool.get("monitor")
	ids := reviewIDs(t, c, id)
	act := func(action string, picked ...string) (int, map[string]any) {
		d, _ := c.nodes.Read(id, 0)
		return gesture(t, tw, csrf, "POST", "/api/nodes/"+id+"/act", nodeActIn{Block: "tools", List: "root", Action: action, Version: d.Version.N, IDs: picked})
	}
	if code, out := act("accept", ids["status"]); code != http.StatusOK || out["text"] != "1 accepté" {
		t.Fatalf("accept: %d %v", code, out)
	}
	if slices.Contains(u.heldTools(), "status") || u.tool("status") == nil {
		t.Errorf("status still held: %v", u.heldTools())
	}
	rows, _ := c.store.decisions(0, 5)
	if len(rows) == 0 || rows[0].By != byUser || rows[0].Tool != "mcp__eriphe-catalog__catalog_accept" || !strings.Contains(rows[0].Reason, "seul son geste") {
		t.Fatalf("journal: %+v", rows)
	}
	raw, _ := c.nodes.Trace(id)
	if !strings.Contains(string(raw), "Accepter 1 élément (eriphe-catalog/catalog_accept)") {
		t.Errorf("trace: %s", raw)
	}
	c.live.wg.Wait()
	if code, out := act("refuse", ids["alerts"], ids["gone"]); code != http.StatusOK || out["text"] != "2 refusés" {
		t.Fatalf("refuse: %d %v", code, out)
	}
	if !slices.Contains(u.heldTools(), "alerts") {
		t.Error("a refused tool is lent")
	}
	if pins, _ := c.store.toolPins("monitor"); pins["gone"] == "" {
		t.Error("a refused drop forgot its pin")
	}
	if left := c.toolChanges("fr"); len(left) != 0 {
		t.Errorf("left to review: %+v", left)
	}
	c.live.wg.Wait()
	c.readNode(context.Background(), id, nil, time.Now())
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
		if strings.Contains(m.Text, "monitor") || strings.Contains(m.Text, "outil") {
			t.Errorf("the brain is told: %+v", m)
		}
	}
}

// Only the user's gesture accepts or refuses: the brain, a task, an
// approved card, even with a yes given before, are refused; the changes
// are read by code alone, lent to no agent.
func TestToolReviewNeverForAgents(t *testing.T) {
	tw, _, _ := reviewWorld(t)
	c := tw.c
	ctx := context.Background()
	review := c.pool.get(reviewServer)
	for _, tool := range []string{"catalog_accept", "catalog_refuse"} {
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
		if _, _, code, _ := c.catalogCall(ctx, name, `{"ids":[]}`, false, func(map[string]any) {}); code != callWhyNotAllowed {
			t.Errorf("%s: an approved card's call: %q", tool, code)
		}
		if slices.ContainsFunc(c.lentTools(), func(x catalogTool) bool { return x.server == reviewServer && x.tool == tool }) {
			t.Errorf("%s is lent", tool)
		}
	}
	if len(c.toolChanges("fr")) != 3 {
		t.Error("an agent's call settled something")
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
	id := mustNode(t, c, nodestore.New{Title: "Ailleurs", Lifetime: nodefile.Standing, Body: "x\n"})
	_, err := c.authorWrite(id, lastHash(t, c, id), byBrain, false, c.viewSourceOK(""), func(n *nodefile.Node) error {
		return n.AuthorBody("x\n\n" + listBlock("b", "ch", map[string]any{"tool": reviewServer + "/catalog_changes", "items": map[string]any{"path": "changes", "id": "id"}}))
	})
	if err == nil {
		t.Error("the brain writes a block reading the changes")
	}
}

// A tool only the user's gesture runs acts on its own server's list: an
// action that hands it another source's ids is refused, nothing done.
func TestToolReviewActOnItsOwnList(t *testing.T) {
	tw, csrf, fm, id := actionsWeb(t, `{"accept": {"tool": "eriphe-catalog/catalog_accept", "items": "ids", "source": "received"}}`)
	_ = fm
	waitStatus(t, tw.c.pool.get(reviewServer), statusReady)
	d, _ := tw.c.nodes.Read(id, 0)
	code, out := gesture(t, tw, csrf, "POST", "/api/nodes/"+id+"/act", nodeActIn{Block: "inbox", List: "root", Action: "accept", Version: d.Version.N, IDs: []string{"m1"}})
	if code != http.StatusForbidden {
		t.Fatalf("act on another list: %d %v", code, out)
	}
	if rows, _ := tw.c.store.decisions(0, 5); len(rows) != 0 {
		t.Errorf("journal: %+v", rows)
	}
}
