package main

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"
)

// What a call does to what, in plain words, nothing of a server's own: a
// verb of ours for a usual tool name, a move to a usual place said as
// such, else the tool's title, else its name; the items counted, of the
// kind its server or source names, else « éléments ».
func TestCallPhrase(t *testing.T) {
	ids := func(xs ...string) []any {
		out := []any{}
		for _, x := range xs {
			out = append(out, x)
		}
		return out
	}
	for _, tc := range []struct {
		tool, title string
		input       map[string]any
		labels      []itemLabel
		lang, want  string
	}{
		{"mcp__mail__trash", "", map[string]any{"account": "perso", "ids": ids("a", "b")}, nil, "fr", "Mettre 2 mails à la corbeille"},
		{"mcp__mail__trash", "", map[string]any{"ids": ids("a")}, nil, "en", "Move 1 email to the trash"},
		{"mcp__mail__delete", "", map[string]any{"ids": ids("a", "b")}, nil, "fr", "Supprimer 2 mails"},
		{"mcp__mail__move", "", map[string]any{"ids": ids("a"), "to": "Archive"}, nil, "fr", "Archiver 1 mail"},
		{"mcp__mail__move", "", map[string]any{"ids": ids("a", "b"), "to": "Factures"}, nil, "fr", "Déplacer 2 mails → Factures"},
		{"mcp__calendar__delete", "", map[string]any{"event_ids": ids("e1")}, nil, "fr", "Supprimer 1 événement"},
		// The kind from the source that lists them, the server saying none.
		{"mcp__box__remove", "", map[string]any{"ids": ids("f1", "f2")}, []itemLabel{{ID: "f1", Source: "files"}, {ID: "f2", Source: "files"}}, "fr", "Retirer 2 fichiers"},
		// Neither: « éléments ».
		{"mcp__box__remove", "", map[string]any{"ids": ids("x", "y", "z")}, nil, "fr", "Retirer 3 éléments"},
		// No verb of ours: the tool's title, then its name.
		{"mcp__box__file_away", "Ranger", map[string]any{"ids": ids("x", "y")}, nil, "fr", "Ranger · 2 éléments"},
		{"mcp__box__file_away", "", map[string]any{"ids": ids("x", "y")}, nil, "fr", "Utiliser « file_away » sur 2 éléments"},
		// One item it names, by a title: said by itself.
		{"mcp__mail__reply", "Envoyer la réponse", map[string]any{"id": "a1", "body": "Merci"}, []itemLabel{{ID: "a1", Title: "Devis", Detail: "Sam"}}, "fr", "Envoyer la réponse · « Devis » · Sam"},
		// Nothing by id: the action alone.
		{"mcp__github__merge_pull_request", "", map[string]any{"pullNumber": 3}, nil, "fr", "Fusionner une pull request"},
		{"mcp__mail__send", "Envoyer un mail", map[string]any{"to": "sam@example.org"}, nil, "fr", "Envoyer un mail"},
		{"mcp__ops__rollback", "", map[string]any{"version": "2.0.0"}, nil, "fr", "Utiliser « rollback »"},
	} {
		server, _, _ := splitToolName(tc.tool)
		if got := callPhrase(tc.tool, tc.title, tc.input, tc.labels, nounOf(server, tc.labels), tc.lang, 0); got != tc.want {
			t.Errorf("%s %v: %q, want %q", tc.tool, tc.input, got, tc.want)
		}
	}
	// A count given: calls that do the same to other items, together.
	if got := callPhrase("mcp__mail__trash", "", map[string]any{"ids": ids("a")}, nil, "mail", "fr", 4); got != "Mettre 4 mails à la corbeille" {
		t.Errorf("counted: %q", got)
	}
}

// The items a call acts on, by their titles: five at most, then how many
// more; a title someone else wrote keeps its quiet mark.
func TestLabelLines(t *testing.T) {
	var ls []itemLabel
	for _, s := range []string{"Un", "Deux", "Trois", "Quatre", "Cinq", "Six", "Sept"} {
		ls = append(ls, itemLabel{ID: s, Title: s, Detail: "Sam"})
	}
	got := labelLines(ls, 9, "fr")
	if len(got) != 6 || got[0] != "« Un » · Sam" || got[5] != "et 4 autres" {
		t.Errorf("lines %q", got)
	}
	if got := labelLines(ls[:2], 2, "en"); !slices.Equal(got, []string{"“Un” · Sam", "“Deux” · Sam"}) {
		t.Errorf("two %q", got)
	}
	if got := labelLine(itemLabel{ID: "x", Title: "Payez 1,99 €", External: true}, "fr"); got != "« Payez 1,99 € » ⚠" {
		t.Errorf("external %q", got)
	}
	if got := labelLines(nil, 3, "fr"); len(got) != 0 {
		t.Errorf("no title known, no line: %q", got)
	}
}

// brainCallOn has the brain call a tool held on a card.
func brainCallOn(t *testing.T, c *core, pid, tool string, input map[string]any) {
	t.Helper()
	c.store.addEvents("b-1", []EventRow{{Seq: len(pid), Time: time.Now(), Type: "permission",
		Data: map[string]any{"id": pid, "tool_name": tool, "input": input}}})
	if err := c.askBrainAccess(context.Background(), &TaskRow{ID: "b-1"}, pid, tool, input, nil); err != nil {
		t.Fatal(err)
	}
}

// The brain, asked in conversation to act on items a view lists, calls
// the tool on their ids: its card is that view's, says what it does to
// which mails by their titles (someone else's words, marked), its exact
// call under the one Détails; it waits on those rows, one card shared,
// and not at the page's foot. A call that names no row keeps its card
// apart. Refused, it leaves the rows.
func TestBrainCallLandsOnItsRows(t *testing.T) {
	c, node, _ := mailView(t)
	w := &web{c: c}
	c.store.insertTask(&TaskRow{ID: "b-1", Kind: "brain", Device: "box", Runtime: "claude-code", State: stateWaitingPermission})
	brainCallOn(t, c, "p-1", "mcp__monitor__move", map[string]any{"account": "sam@example.org", "ids": []any{"m1", "m2"}, "to": "archive"})

	var id int64
	c.store.db.QueryRow(`SELECT id FROM access_asks WHERE permission_id = 'p-1'`).Scan(&id)
	a, err := c.accessAskOf(id)
	if err != nil || a.Node != node || a.Source != "mails" || len(a.Labels) != 2 {
		t.Fatalf("the call %+v, %v", a, err)
	}
	ref := proposalRef(cardAccess, id)
	if !slices.Contains(c.cardNodes(ref), node) {
		t.Errorf("the card is not the view's: %v", c.cardNodes(ref))
	}
	p := c.proposalOf(cardAccess, id)
	if got := p.Text("fr"); got != "Archiver 2 mails" {
		t.Errorf("text %q", got)
	}
	if got := p.SummaryIn("fr"); !slices.Equal(got, []string{"« STAGE – Data Engineer » · Emplois Example ⚠", "« Votre lettre de la semaine » · news@example.org ⚠"}) {
		t.Errorf("summary %q", got)
	}
	if got := p.ContextIn("fr"); got != "compte sam@example.org" {
		t.Errorf("context %q", got)
	}
	fs := p.FieldsIn("fr")
	if len(fs) != 1 || !fs[0].Detail || fs[0].Name != "monitor/move" || !strings.Contains(fs[0].Value, `ids : ["m1","m2"]`) || !strings.Contains(fs[0].Value, "to : archive") {
		t.Errorf("details %+v", fs)
	}

	// On both rows, one card; not at the foot.
	cards := c.itemCards(node, "mails")
	if cards["m1"] != ref || cards["m2"] != ref {
		t.Errorf("rows %v", cards)
	}
	l, _ := pageListOf(t, w, node, "recus", "root")
	for _, it := range l.Items {
		if it.Card != ref {
			t.Errorf("row %s carries %q", it.ID, it.Card)
		}
	}
	pg, _ := w.nodePageOf(node, 0, "fr")
	if slices.ContainsFunc(pg.Cards, func(v proposalView) bool { return v.Ref == ref }) {
		t.Errorf("the foot shows it: %+v", pg.Cards)
	}

	// A call naming no row: no node of its own, on no row.
	brainCallOn(t, c, "p-22", "mcp__monitor__move", map[string]any{"account": "sam@example.org", "ids": []any{"zz9"}, "to": "archive"})
	var other int64
	c.store.db.QueryRow(`SELECT id FROM access_asks WHERE permission_id = 'p-22'`).Scan(&other)
	if o, _ := c.accessAskOf(other); o.Node != "" || len(o.Labels) != 0 {
		t.Errorf("a call on no row: %+v", o)
	}
	if got := c.proposalOf(cardAccess, other).Text("fr"); got != "Archiver 1 élément" {
		t.Errorf("its words %q", got)
	}

	// Refused: off its rows.
	x := cardOf(t, c, cardAccess, id)
	if _, err := c.decideCard(context.Background(), x.ID, false, "web"); err != nil {
		t.Fatal(err)
	}
	if cards := c.itemCards(node, "mails"); cards["m1"] != "" || cards["m2"] != "" {
		t.Errorf("rows after a no %v", cards)
	}
}

// A shared card's Oui, on any of its rows: the call runs once, its rows
// carry no card any more, and its items are shown done at once
// (source_fresh.go), until their source is read again. The call runs
// beside the yes (runApprovedCall): its row keeps the card, running,
// until it is done.
func TestBrainCallOnRowsYesMarksThemDone(t *testing.T) {
	c, node, _ := mailView(t)
	c.store.insertTask(&TaskRow{ID: "b-1", Kind: "brain", Device: "box", Runtime: "claude-code", State: stateWaitingPermission})
	brainCallOn(t, c, "p-1", "mcp__monitor__move", map[string]any{"account": "sam@example.org", "ids": []any{"m1", "m2"}, "to": "archive"})
	var id int64
	c.store.db.QueryRow(`SELECT id FROM access_asks WHERE permission_id = 'p-1'`).Scan(&id)
	if cards := c.itemCards(node, "mails"); cards["m1"] == "" || cards["m1"] != cards["m2"] {
		t.Fatalf("one card on both rows: %v", cards)
	}
	if _, err := c.decideCard(context.Background(), cardOf(t, c, cardAccess, id).ID, true, "web"); err != nil {
		t.Fatal(err)
	}
	c.yeses.Wait() // the call ran
	if got := states(t, c, node, "recus", "mails"); got["m1"] != itemDone || got["m2"] != itemDone {
		t.Errorf("its items at once: %v", got)
	}
	if cards := c.itemCards(node, "mails"); cards["m1"] != "" || cards["m2"] != "" {
		t.Errorf("rows after its yes %v", cards)
	}
	c.live.wg.Wait()
}
