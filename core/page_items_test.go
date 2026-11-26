package main

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// refsPage is a page of mails that references the faked server's m1 and
// m2 in its rows, m3 in a paragraph, and m1 again on another server's
// line (agenda:m1: another item).
const refsPage = `## À archiver
- [Facture de Sam][m1] : payée
- [Pub Maison Lune · Gift Guide][m2] : publicité
  - Détail du [même mail][m2]
- [Le rendez-vous][a1] : à garder

Un [paragraphe][m3].

[m1]: monitor:m1
[m2]: monitor:m2
[m3]: monitor:m3
[a1]: agenda:m1
`

// refsCore is a core whose faked server holds m1 to m3 (m2 Sam's keys,
// which the page calls « Pub »), a second catalog server « agenda » of
// the same tools, and a node whose page is refsPage.
func refsCore(t *testing.T) (*core, *fakeMonitor, string) {
	t.Helper()
	c, fm, _ := viewCore(t)
	fm.mu.Lock()
	fm.got = []map[string]any{
		{"id": "m1", "subject": "Facture de novembre", "from": "Sam <sam@example.org>"},
		{"id": "m2", "subject": "Les clés pour samedi", "from": "Sam <sam@example.org>"},
		{"id": "m3", "subject": "Lettre", "from": "news@example.org"},
	}
	fm.mu.Unlock()
	c.pool.sync(setServer(c, "agenda", func(s *Server) {
		*s = Server{URL: "https://agenda.example.org/mcp", Auth: &ServerAuth{None: true}, Use: "the agenda", Access: accessReads}
	}))
	waitStatus(t, c.pool.get("agenda"), statusReady)
	id := mustNode(t, c, nodestore.New{Title: "Mes mails", Body: refsPage})
	return c, fm, id
}

// keptIn keeps a call in node as a run's would, its card shown.
func keptIn(t *testing.T, c *core, node, pid, tool string, input map[string]any) accessQuestion {
	t.Helper()
	a := c.keepCall(context.Background(), keeper{task: "t-x", pid: pid, node: node}, tool, input)
	if a.ID == 0 {
		t.Fatalf("%s not kept", tool)
	}
	c.cardNow(cardCalls, a.Batch, "", nodeRef(node))
	return a
}

// itemsOn are the page's items of a line.
func itemsOn(items []pageItem, line int) []pageItem {
	var out []pageItem
	for _, it := range items {
		if it.Line == line {
			out = append(out, it)
		}
	}
	return out
}

// A call on monitor:m1 waits on the line that references it, its words
// the mail as its server read it; a second on m2 folds into the same card,
// on m2's row; neither takes the line of agenda:m1, another server's item,
// nor goes to the foot. The label « Pub » on Sam's mail is misleading.
func TestCardsOnReferencedLines(t *testing.T) {
	c, fm, node := refsCore(t)
	move := func(ids ...string) map[string]any { return moveMail("sam@example.org", "archive", ids...) }
	a := keptIn(t, c, node, "p1", "mcp__monitor__move", move("m1"))
	b := keptIn(t, c, node, "p2", "mcp__monitor__move", move("m2"))
	if len(a.Labels) != 1 || a.Labels[0] != (itemLabel{ID: "m1", Server: "monitor", Title: "Facture de novembre", Detail: "Sam <sam@example.org>", External: true}) {
		t.Fatalf("m1's labels %+v", a.Labels)
	}
	if a.Node != node || a.Item != "m1" {
		t.Fatalf("kept %+v", a)
	}
	if got := fm.opened; !slices.Contains(got, "message:m1") || !slices.Contains(got, "message:m2") {
		t.Fatalf("the items were not read by the server's declared read: %v", got)
	}
	c.foldCalls()
	if batchOf(c, b.ID) != a.Batch {
		t.Fatalf("m2's call did not fold: %d on %d", b.ID, batchOf(c, b.ID))
	}
	ref := proposalRef(cardCalls, a.Batch)
	p := c.proposalOf(cardCalls, a.Batch)
	if got := p.Text("fr"); got != moveWords("2 mails") {
		t.Errorf("text %q", got)
	}
	var lines []string
	for _, ch := range p.choices("fr") {
		lines = append(lines, ch.Text)
	}
	if !slices.Equal(lines, []string{"« Facture de novembre » · Sam <sam@example.org> ⚠", "« Les clés pour samedi » · Sam <sam@example.org> ⚠"}) {
		t.Errorf("its lines say %q: the mails as read, never the page's words", lines)
	}
	if strings.Contains(p.Text("fr")+strings.Join(lines, ""), "Pub") {
		t.Error("the card says the link's words")
	}

	pg, err := (&web{c: c}).nodePageOf(node, 0, "fr")
	if err != nil {
		t.Fatal(err)
	}
	m1, m2, m2d, a1, m3 := itemsOn(pg.Items, 2), itemsOn(pg.Items, 3), itemsOn(pg.Items, 4), itemsOn(pg.Items, 5), itemsOn(pg.Items, 7)
	if len(m1) != 1 || m1[0].Card != ref || m1[0].Row != 2 || m1[0].ID != "m1" || m1[0].Misleading || !m1[0].Open {
		t.Errorf("m1's line %+v", m1)
	}
	if len(m2) != 1 || m2[0].Card != ref || m2[0].Row != 3 || !m2[0].Misleading || m2[0].Title != "Les clés pour samedi" {
		t.Errorf("m2's line %+v: Sam's mail, its label misleading", m2)
	}
	if len(m2d) != 1 || m2d[0].Row != 3 || m2d[0].Card != ref {
		t.Errorf("m2 in its row's detail %+v", m2d)
	}
	if len(a1) != 1 || a1[0].Card != "" || a1[0].ID != "m1" {
		t.Errorf("agenda:m1 took monitor's card: %+v", a1)
	}
	if len(m3) != 1 || m3[0].Row != 0 || m3[0].Card != "" {
		t.Errorf("a paragraph's reference %+v", m3)
	}
	if !slices.Equal(pg.Placed, []string{ref}) || slices.ContainsFunc(pg.Cards, func(v proposalView) bool { return v.Ref == ref }) {
		t.Errorf("placed %v, at the foot %+v", pg.Placed, pg.Cards)
	}

	// A call of agenda on m1 is agenda:m1's, on its own line.
	x := keptIn(t, c, node, "p3", "mcp__agenda__move", move("m1"))
	if len(x.Labels) != 1 || x.Labels[0].Server != "agenda" {
		t.Fatalf("agenda's labels %+v", x.Labels)
	}
	pg, _ = (&web{c: c}).nodePageOf(node, 0, "fr")
	if a1 := itemsOn(pg.Items, 5); len(a1) != 1 || a1[0].Card != proposalRef(cardCalls, x.Batch) {
		t.Errorf("agenda's card %+v", a1)
	}
	if m1 := itemsOn(pg.Items, 2); m1[0].Card != ref {
		t.Errorf("m1's card moved: %+v", m1)
	}
}

// A call on an item referenced only in a paragraph waits at the foot: no
// line carries it.
func TestCardOnAParagraphReferenceAtTheFoot(t *testing.T) {
	c, _, node := refsCore(t)
	a := keptIn(t, c, node, "p1", "mcp__monitor__move", moveMail("sam@example.org", "archive", "m3"))
	if len(a.Labels) != 1 || a.Labels[0].Title != "Lettre" {
		t.Fatalf("m3's labels %+v", a.Labels)
	}
	ref := proposalRef(cardCalls, a.Batch)
	pg, _ := (&web{c: c}).nodePageOf(node, 0, "fr")
	if len(pg.Placed) != 0 || !slices.ContainsFunc(pg.Cards, func(v proposalView) bool { return v.Ref == ref }) {
		t.Errorf("placed %v, foot %+v", pg.Placed, pg.Cards)
	}
}

// An item its server does not read freely is counted, never named from
// the link's words: its line says its words are not checked.
func TestUnreadItemIsNotNamed(t *testing.T) {
	c, fm, node := refsCore(t)
	c.pool.sync(setServer(c, "monitor", func(s *Server) { s.Exceptions = map[string]string{"message": gridNever} }))
	waitStatus(t, c.pool.get("monitor"), statusReady)
	a := keptIn(t, c, node, "p1", "mcp__monitor__move", moveMail("sam@example.org", "archive", "m2"))
	if len(a.Labels) != 1 || a.Labels[0].Title != "" || a.Labels[0].Server != "monitor" {
		t.Fatalf("labels %+v", a.Labels)
	}
	if got := openedNow(fm); len(got) != 0 {
		t.Fatalf("a read never allowed ran: %v", got)
	}
	if strings.Contains(c.proposalOf(cardCalls, a.Batch).Text("fr"), "Pub") {
		t.Error("the card says the link's words")
	}
	pg, _ := (&web{c: c}).nodePageOf(node, 0, "fr")
	if m2 := itemsOn(pg.Items, 3); len(m2) != 1 || !m2[0].Unread || m2[0].Misleading || m2[0].Card == "" {
		t.Errorf("m2's line %+v", m2)
	}
}

// A card: link places only one of the node's own cards.
func TestCardLinkPlacesOnlyTheNodesCard(t *testing.T) {
	c, _, node := refsCore(t)
	a := keptIn(t, c, node, "p1", "mcp__monitor__rollback", map[string]any{"version": "1.0"})
	ref := proposalRef(cardCalls, a.Batch)
	other := mustNode(t, c, nodestore.New{Title: "Ailleurs", Body: "Rien.\n"})
	b := keptIn(t, c, other, "p2", "mcp__monitor__rollback", map[string]any{"version": "2.0"})
	theirs := proposalRef(cardCalls, b.Batch)
	body := refsPage + "\n[Revenir](card:" + ref + ") · [Leur carte](card:" + theirs + ")\n"
	if _, err := c.nodes.ChangeLast(node, byBrain, func(n *nodefile.Node) error { return n.AuthorBody(body) }); err != nil {
		t.Fatal(err)
	}
	pg, _ := (&web{c: c}).nodePageOf(node, 0, "fr")
	if !slices.Equal(pg.Placed, []string{ref}) {
		t.Errorf("placed %v, want only %s", pg.Placed, ref)
	}
	if slices.ContainsFunc(pg.Cards, func(v proposalView) bool { return v.Ref == ref }) {
		t.Errorf("%s at the foot too", ref)
	}
}

// A call the brain kept on no item goes where the brain's card: line is,
// its record too: it does not come back to its first node at a start.
func TestCardLineTakesBrainCall(t *testing.T) {
	c, _, node := refsCore(t)
	a := c.keepCall(context.Background(), keeper{task: "b-1", pid: "p1", node: node, turn: true}, "mcp__monitor__rollback", map[string]any{"version": "1.0"})
	ref := proposalRef(cardCalls, a.Batch)
	other := mustNode(t, c, nodestore.New{Title: "Le retour en arrière", Body: "On en parle ici.\n"})
	if _, err := c.authorWriteOn(other, onLast, byBrain, false, nodeAppend("[Revenir à la 1.0](card:"+ref+")")); err != nil {
		t.Fatal(err)
	}
	c.syncCards()
	if got := c.cardNodes(ref); !slices.Equal(got, []string{other}) {
		t.Fatalf("linked to %v, want only %s", got, other)
	}
	if b, _ := c.accessAskOf(a.ID); b.Node != other {
		t.Errorf("its call's node is %q", b.Node)
	}
}

// Opening a referenced mail reads it by the server's declared read, the
// reference found in the stored page by its label; the page's misleading
// words say so. A label the page does not have, or one of a server that
// declares no read, opens nothing.
func TestOpenReferencedItem(t *testing.T) {
	c, fm, node := refsCore(t)
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	code, out := gesture(t, tw, csrf, "POST", "/api/nodes/"+node+"/open", itemOpenIn{Ref: "M2"})
	if code != http.StatusOK || out["title"] != "Les clés pour samedi" || out["from"] != "Sam <sam@example.org>" || out["body"] != "Le texte de m2" || out["misleading"] != true || out["external"] != true {
		t.Fatalf("open m2: %d %v", code, out)
	}
	code, out = gesture(t, tw, csrf, "POST", "/api/nodes/"+node+"/open", itemOpenIn{Ref: "m1"})
	if code != http.StatusOK || out["title"] != "Facture de novembre" || out["misleading"] != nil {
		t.Fatalf("open m1: %d %v", code, out)
	}
	if code, _ := gesture(t, tw, csrf, "POST", "/api/nodes/"+node+"/open", itemOpenIn{Ref: "m9"}); code != http.StatusConflict {
		t.Fatalf("a label the page lacks: %d", code)
	}
	if got := openedNow(fm); !slices.Equal(got, []string{"message:m2", "message:m1"}) {
		t.Fatalf("calls %v", got)
	}
	d, _ := c.nodes.Read(node, 0)
	if raw, _ := c.nodes.Trace(node); strings.Contains(string(d.Raw), "Le texte") || strings.Contains(string(raw), "Le texte") {
		t.Fatal("what was read was kept")
	}
}

// What a reference's words say of the item read: one word of three
// letters or more found is enough; nothing to tell, nothing said.
func TestMisleading(t *testing.T) {
	sam := itemLabel{Title: "Les clés pour samedi", Detail: "Sam <sam@example.org>"}
	for words, want := range map[string]bool{
		"Pub Maison Lune · Gift Guide": true,
		"Pub":                          true,
		"Sam · Devis terrasse":         false,
		"Les CLES":                     false,
		"m2":                           false,
		"":                             false,
	} {
		if got := misleading(words, sam); got != want {
			t.Errorf("%q: %v, want %v", words, got, want)
		}
	}
	if misleading("Pub", itemLabel{}) {
		t.Error("an item not read is said to mislead")
	}
}
