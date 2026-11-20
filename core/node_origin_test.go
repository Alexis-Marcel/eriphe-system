package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// Who wanted a node, as code recorded it (node_origin.go).

// originNow is a node's origin, its graph row and its page's line.
func originNow(t *testing.T, c *core, id string) (*nodefile.Origin, graphNode, pageOrigin) {
	t.Helper()
	sum, err := c.nodes.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	p, err := originWeb(t, c).nodePageOf(id, 0, "fr")
	if err != nil {
		t.Fatal(err)
	}
	return c.originOf(id), c.graphRow(sum), p.Origin
}

// A subject the user asks for at the home is theirs: their message is
// quoted, no sign; the task it runs and what that task opens are theirs
// too, steps included.
func TestUserTurnOpensAskedNodes(t *testing.T) {
	c := graphCore(t)
	seq, err := c.store.addUserMessage("Tu peux garder un œil sur mes mails ?\nMerci", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	asked := int(seq)
	c.turn.start(asked, asked, 0)
	c.store.markDelivered(asked)
	out, err := c.brainNodeOpen(context.Background(), brainNodeOpenIn{Title: "Garder un œil sur mes mails"})
	if err != nil {
		t.Fatal(err)
	}
	subject := out["id"].(string)
	o, g, line := originNow(t, c, subject)
	if o == nil || o.By != nodefile.OpenedUser || o.Msg != asked || o.Task != "" {
		t.Fatalf("origin %+v", o)
	}
	if g.Unasked || line.Unasked || line.Text != "Ouvert à ta demande : « Tu peux garder un œil sur mes mails ? »" {
		t.Errorf("row %+v, line %+v", g, line)
	}

	task, err := c.runTask(context.Background(), runRequest{Title: "Trier", Prompt: "Trie les mails.", Node: subject, byBrain: true})
	if err != nil {
		t.Fatal(err)
	}
	mine := c.taskNode(task.ID)
	if o, g, _ := originNow(t, c, mine); o == nil || o.By != nodefile.OpenedUser || o.Msg != asked || o.Task != task.ID || o.Node != subject || g.Unasked {
		t.Fatalf("the task's node %+v", o)
	}
	kid := mustNodes(t, c, task.ID, "node_open", map[string]any{"title": "Pages", "goal": "Les pages sont écrites.",
		"steps": []any{map[string]any{"key": "a", "title": "A", "prompt": "Fais A."}}})
	if o, g, _ := originNow(t, c, kid["id"].(string)); o == nil || o.By != nodefile.OpenedUser || o.Task != task.ID || o.Node != mine || g.Unasked {
		t.Fatalf("the task's child %+v", o)
	}
	step := kid["steps"].(map[string]any)["a"].(string)
	if o, g, _ := originNow(t, c, step); o == nil || o.By != nodefile.OpenedUser || o.Node != kid["id"] || g.Unasked {
		t.Fatalf("its step %+v", o)
	}

	// The message withdrawn or gone: the page says it was asked, unquoted.
	c.store.db.Exec(`DELETE FROM messages WHERE seq = ?`, asked)
	if _, _, line := originNow(t, c, subject); line.Text != "Ouvert à ta demande." {
		t.Errorf("gone %+v", line)
	}
}

// A turn of events only (a wake, a monitor's event) opens a node the user
// did not ask for: it has the sign, and its page says so.
func TestEventTurnOpensUnasked(t *testing.T) {
	c := graphCore(t)
	c.turn.start(0, 0, 3)
	id := mustBrainNodes(t, c, "node_open", map[string]any{"title": "Alerte disque"})["id"].(string)
	o, g, line := originNow(t, c, id)
	if o == nil || o.By != nodefile.OpenedEvent {
		t.Fatalf("origin %+v", o)
	}
	if !g.Unasked || !line.Unasked || line.Text != "Ouvert après un événement, sans demande de ta part." {
		t.Errorf("row %+v, line %+v", g, line)
	}
	if p, _ := originWeb(t, c).nodePageOf(id, 0, "en"); p.Origin.Text != "Opened after an event, without a request of yours." {
		t.Errorf("en %+v", p.Origin)
	}
}

// A skill's run opens its task's node, and what that task opens: the
// skill and what started it, the sign on each.
func TestSkillRunOpensUnasked(t *testing.T) {
	c, now := skillCore(t)
	openGraph(t, c)
	addTriggered(t, c, "---\nname: mails-rdv\ndescription: les rendez-vous des mails\nwhen: monitor.new\ntools: [monitor]\n---\nRepère les rendez-vous.\n")
	c.emit(journalEvent{Kind: "monitor.new", Key: "perso", Source: "monitor:mails", At: *now, Payload: map[string]any{"subject": "RDV le 8"}})
	c.tickSkills(context.Background(), now.Add(time.Minute))
	rs := runsOfSkill(t, c, "mails-rdv")
	if len(rs) != 1 || rs[0].Task == "" {
		t.Fatalf("runs %+v", rs)
	}
	task := rs[0].Task
	mine := c.taskNode(task)
	o, g, line := originNow(t, c, mine)
	if o == nil || o.By != nodefile.OpenedSkill || o.Skill != "mails-rdv" || o.On != "monitor:mails" || o.Task != task {
		t.Fatalf("the run's node %+v", o)
	}
	if !g.Unasked || line.Text != "Ouvert par le skill mails-rdv, après ce qu'a vu la surveillance « mails », sans demande de ta part." {
		t.Errorf("row %+v, line %+v", g, line)
	}
	kid := mustNodes(t, c, task, "node_open", map[string]any{"title": "Rendez-vous du 8"})["id"].(string)
	if o, g, _ := originNow(t, c, kid); o == nil || o.By != nodefile.OpenedSkill || o.Skill != "mails-rdv" || o.Task != task || o.Node != mine || !g.Unasked {
		t.Fatalf("its child %+v", o)
	}
}

// The user's yes on a view's card makes its node: theirs. A node made
// before origins were recorded says none: no sign, an unknown origin.
func TestViewAndUnknownOrigins(t *testing.T) {
	c, node, _ := mailView(t)
	if o, g, line := originNow(t, c, node); o == nil || o.By != nodefile.OpenedUser || !strings.HasPrefix(o.Card, "view:") ||
		g.Unasked || line.Text != "Ouvert à ta demande : tu as dit oui à sa carte." {
		t.Errorf("a view %+v, %+v", o, line)
	}
	review, err := c.ensureToolReview()
	if err != nil {
		t.Fatal(err)
	}
	if o, g, line := originNow(t, c, review); o == nil || o.By != nodefile.OpenedEriphe || g.Unasked || line.Unasked ||
		line.Text != "Une place d'Eriphe, ouverte par lui à son démarrage." {
		t.Errorf("Eriphe's place %+v, %+v", o, line)
	}
	d, err := c.nodes.Create(nodestore.New{Title: "D'avant", Author: byBrain})
	if err != nil {
		t.Fatal(err)
	}
	if o, g, line := originNow(t, c, d.Node.Meta.ID); o != nil || g.Unasked || line.Unasked || line.Text != "Origine inconnue." {
		t.Errorf("unknown %+v, %+v", o, line)
	}
}

// Code's writes keep opened_by: an author's write, its marks, archiving.
func TestOpenedByOutlivesWrites(t *testing.T) {
	c := graphCore(t)
	c.turn.start(0, 0, 3)
	id := mustBrainNodes(t, c, "node_open", map[string]any{"title": "Alerte disque"})["id"].(string)
	d, _ := c.nodes.Read(id, 0)
	mustBrainNodes(t, c, "node_write", map[string]any{"id": id, "expect": d.Version.Hash, "body": "Le disque est plein."})
	mustBrainNodes(t, c, "node_set", map[string]any{"id": id, "title": "Disque plein"})
	c.markNodeExternal(id, byBrain)
	if err := c.archiveNode(context.Background(), id, byUser); err != nil {
		t.Fatal(err)
	}
	if o := c.originOf(id); o == nil || o.By != nodefile.OpenedEvent {
		t.Fatalf("after writes %+v", o)
	}
	if sum, _ := c.nodes.Get(id); !sum.Unasked() {
		t.Errorf("the index forgot it: %+v", sum)
	}
}
