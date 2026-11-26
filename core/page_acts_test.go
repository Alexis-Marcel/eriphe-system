package main

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// actPage is a page of mails with an action on its first heading: m1
// and m2 under it, m3 ticked, m4 under the next heading, m5 in a
// paragraph, a1 another server's item.
const actPage = `## À jeter
[Jeter ces mails](do:monitor/discard)

- [Facture de Sam][m1] : payée
- [Pub Maison Lune][m2] : publicité
- [x] [Lettre][m3]
- [Rendez-vous][a1]

Un [paragraphe][m5].

## À garder
- [Autre][m4]

[m1]: monitor:m1
[m2]: monitor:m2
[m3]: monitor:m3
[m4]: monitor:m4
[m5]: monitor:m5
[a1]: agenda:m1
`

// actLine names the do: line of actPage.
var actLine = nodefile.LineRef{Line: 2, Text: "[Jeter ces mails](do:monitor/discard)"}

// actCore is refsCore with a node whose page is body, written by author.
func actCore(t *testing.T, body, author string) (*core, *fakeMonitor, string) {
	t.Helper()
	c, fm, _ := refsCore(t)
	id := mustNode(t, c, nodestore.New{Title: "Tri", Body: body, Author: author})
	return c, fm, id
}

func actItemIDs(a pageAct) []string {
	var out []string
	for _, it := range a.Items {
		out = append(out, it.ID)
	}
	return out
}

func discarded(fm *fakeMonitor) []string {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	return slices.Clone(fm.discarded)
}

// A do: acts on the references of its heading to its tool's server's
// items, the ticked ones left out, as their server reads them; its words
// are code's, never the link's.
func TestActComposedFromItsHeading(t *testing.T) {
	c, _, node := actCore(t, actPage, byBrain)
	a, err := c.composeAct(context.Background(), node, actLine, true, "fr")
	if err != nil {
		t.Fatal(err)
	}
	if a.Refused != "" || !slices.Equal(actItemIDs(a), []string{"m1", "m2"}) {
		t.Fatalf("act %+v", a)
	}
	if a.Items[0].Row != 4 || a.Items[1].Row != 5 || a.Items[0].Title != "Facture de novembre" || a.Items[0].Misleading {
		t.Errorf("m1 %+v", a.Items[0])
	}
	if !a.Items[1].Misleading || a.Items[1].Title != "Les clés pour samedi" {
		t.Errorf("m2 %+v: Sam's mail, its label misleading", a.Items[1])
	}
	if len(a.Acts) != 2 || len(a.Verbs) != 2 {
		t.Fatalf("acts %q verbs %q", a.Acts, a.Verbs)
	}
	for _, s := range slices.Concat(a.Acts, a.Verbs) {
		if strings.Contains(s, "Jeter") || strings.Contains(s, "Pub") || strings.Contains(s, "Facture de Sam") {
			t.Errorf("%q says the page's words", s)
		}
	}
	if !strings.Contains(a.Acts[1], "2") {
		t.Errorf("for 2: %q", a.Acts[1])
	}
}

// Yes runs the items ticked that are still under the heading: one
// removed from the page since, or one added (never shown), does not run;
// the card is a calls card said yes to, on the node; the same items are
// not offered again.
func TestActYesRunsWhatIsStillThere(t *testing.T) {
	c, fm, node := actCore(t, actPage, byBrain)
	drawn, _ := c.composeAct(context.Background(), node, actLine, false, "fr")
	body := strings.Replace(actPage, "- [Pub Maison Lune][m2] : publicité\n", "- [Nouveau][m4]\n", 1)
	if _, err := c.nodes.ChangeLast(node, byBrain, func(n *nodefile.Node) error { return n.AuthorBody(body) }); err != nil {
		t.Fatal(err)
	}
	ref, err := c.actYes(context.Background(), node, actLine, actItemIDs(drawn), "web")
	if err != nil {
		t.Fatal(err)
	}
	c.yeses.Wait()
	if got := discarded(fm); !slices.Equal(got, []string{"m1"}) {
		t.Fatalf("discarded %v, want only m1", got)
	}
	kind, batch, _ := parseProposalRef(ref)
	asks := c.batchAsks(batch)
	if kind != cardCalls || len(asks) != 1 || asks[0].State != startApproved || asks[0].Node != node || asks[0].Labels[0].Title != "Facture de novembre" {
		t.Fatalf("card %s: %+v", ref, asks)
	}
	if xs, _ := c.cardsWhere(`ref = ?`, ref); len(xs) != 1 || xs[0].State != cardApproved {
		t.Errorf("its card %+v", xs)
	}
	if !slices.Contains(c.cardNodes(ref), node) {
		t.Errorf("the card is not the node's: %v", c.cardNodes(ref))
	}
	again, _ := c.composeAct(context.Background(), node, actLine, false, "fr")
	if !slices.Equal(actItemIDs(again), []string{"m4"}) {
		t.Errorf("after the yes, it acts on %v", actItemIDs(again))
	}
	if _, err := c.actYes(context.Background(), node, actLine, []string{"m1"}, "web"); !errors.Is(err, errActNone) {
		t.Errorf("m1 again: %v", err)
	}
}

// The items unticked stay: only those chosen run. A tool that takes one
// id runs once per item.
func TestActYesOnTheItemsChosen(t *testing.T) {
	body := strings.Replace(actPage, "do:monitor/discard", "do:monitor/silence", 1)
	c, fm, node := actCore(t, body, byBrain)
	line := nodefile.LineRef{Line: 2, Text: "[Jeter ces mails](do:monitor/silence)"}
	if _, err := c.actYes(context.Background(), node, line, []string{"m2", "m3", "m4"}, "web"); err != nil {
		t.Fatal(err)
	}
	c.yeses.Wait()
	fm.mu.Lock()
	got := slices.Clone(fm.silenced)
	fm.mu.Unlock()
	if !slices.Equal(got, []string{"m2"}) {
		t.Fatalf("silenced %v, want m2 alone (m3 ticked, m4 elsewhere)", got)
	}

	c2, fm2, node2 := actCore(t, body, byBrain)
	if _, err := c2.actYes(context.Background(), node2, line, []string{"m1", "m2"}, "web"); err != nil {
		t.Fatal(err)
	}
	c2.yeses.Wait()
	fm2.mu.Lock()
	got = slices.Clone(fm2.silenced)
	fm2.mu.Unlock()
	slices.Sort(got)
	if !slices.Equal(got, []string{"m1", "m2"}) {
		t.Fatalf("silenced %v: one call each", got)
	}
}

// A do: whose line its author wrote without the tool acts on nothing,
// even after the user ticked a box of the page (a version of the user's).
func TestActRefusedWhenItsAuthorLacksTheTool(t *testing.T) {
	c, fm, node := actCore(t, actPage, "task:t-none")
	if pr := pageProblems(mustRead(t, c, node), c.lentTool(nil)); !slices.ContainsFunc(pr, func(p nodefile.Problem) bool { return p.Code == nodefile.ProblemDoTool }) {
		t.Errorf("the write's check %v", pr)
	}
	body, _ := mustRead(t, c, node).Node.Body()
	out, err := nodefile.SetTask(body, nodefile.LineRef{Line: 6, Text: "[Lettre][m3]"}, nodefile.TaskOpen)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.nodes.ChangeLast(node, byUser, func(n *nodefile.Node) error { return n.AuthorBody(out) }); err != nil {
		t.Fatal(err)
	}
	a, _ := c.composeAct(context.Background(), node, actLine, false, "fr")
	if a.Refused != actAuthor || len(a.Items) != 0 || a.Why == "" {
		t.Fatalf("act %+v", a)
	}
	if _, err := c.actYes(context.Background(), node, actLine, []string{"m1"}, "web"); !errors.Is(err, errActRefused) {
		t.Fatalf("yes: %v", err)
	}
	if got := discarded(fm); len(got) != 0 {
		t.Fatalf("ran %v", got)
	}

	// The same line written by the user acts.
	c2, _, node2 := actCore(t, actPage, byUser)
	if a, _ := c2.composeAct(context.Background(), node2, actLine, false, "fr"); a.Refused != "" {
		t.Errorf("the user's do: %+v", a)
	}
}

// The access grid applies: a tool it never allows is refused before;
// one whose schema takes more than ids, or a do: out of its place, acts
// on nothing.
func TestActRefusedByGridSchemaOrPlace(t *testing.T) {
	c, fm, node := actCore(t, actPage, byBrain)
	c.pool.sync(setServer(c, "monitor", func(s *Server) { s.Exceptions = map[string]string{"discard": gridNever} }))
	waitStatus(t, c.pool.get("monitor"), statusReady)
	if a, _ := c.composeAct(context.Background(), node, actLine, false, "fr"); a.Refused != actGrid {
		t.Errorf("grid never: %+v", a)
	}
	if _, err := c.actYes(context.Background(), node, actLine, []string{"m1"}, "web"); !errors.Is(err, errActRefused) {
		t.Errorf("yes: %v", err)
	}
	if got := discarded(fm); len(got) != 0 {
		t.Fatalf("ran %v", got)
	}

	for body, want := range map[string]string{
		strings.Replace(actPage, "do:monitor/discard", "do:monitor/move", 1):           actCompose,
		strings.Replace(actPage, "do:monitor/discard", "do:monitor/nope", 1):           actTool,
		strings.Replace(actPage, "## À jeter\n[Jeter", "## À jeter\nVoici. [Jeter", 1): actPlace,
	} {
		c, _, node := actCore(t, body, byBrain)
		_, text, _ := strings.Cut(strings.Split(body, "\n")[1], "Voici. ")
		if text == "" {
			text = strings.Split(body, "\n")[1]
		}
		a, err := c.composeAct(context.Background(), node, nodefile.LineRef{Line: 2, Text: text}, false, "fr")
		if err != nil || a.Refused != want {
			t.Errorf("%s: %+v %v", want, a, err)
		}
	}
}

// The workspace's do: card and its yes, over HTTP.
func TestActOverHTTP(t *testing.T) {
	c, fm, node := actCore(t, actPage, byBrain)
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	code, out := gesture(t, tw, csrf, "POST", "/api/nodes/"+node+"/do", actIn{Line: 2, Text: actLine.Text})
	if code != http.StatusOK || len(out["items"].([]any)) != 2 || out["tool"] != "monitor/discard" {
		t.Fatalf("do: %d %v", code, out)
	}
	if code, _ := gesture(t, tw, csrf, "POST", "/api/nodes/"+node+"/do", actIn{Line: 4, Text: "[Facture de Sam][m1]"}); code != http.StatusConflict {
		t.Errorf("a line that is no do: %d", code)
	}
	code, out = gesture(t, tw, csrf, "POST", "/api/nodes/"+node+"/do/yes", actIn{Line: 2, Text: actLine.Text, Items: []string{"m2"}})
	if code != http.StatusOK || !strings.HasPrefix(out["card"].(string), cardCalls+":") {
		t.Fatalf("yes: %d %v", code, out)
	}
	c.yeses.Wait()
	if got := discarded(fm); !slices.Equal(got, []string{"m2"}) {
		t.Fatalf("discarded %v", got)
	}
	if code, _ := gesture(t, tw, csrf, "POST", "/api/nodes/"+node+"/do/yes", actIn{Line: 2, Text: actLine.Text, Items: []string{"m4"}}); code != http.StatusConflict {
		t.Errorf("an item of another heading: %d", code)
	}
}

// The schema's argument of ids.
func TestActArg(t *testing.T) {
	obj := func(props map[string]any, req ...any) map[string]any {
		m := map[string]any{"type": "object", "properties": props}
		if len(req) > 0 {
			m["required"] = req
		}
		return m
	}
	str := map[string]any{"type": "string"}
	strs := map[string]any{"type": "array", "items": str}
	for _, tc := range []struct {
		schema map[string]any
		arg    string
		list   bool
		ok     bool
	}{
		{obj(map[string]any{"ids": strs, "account": str}, "ids"), "ids", true, true},
		{obj(map[string]any{"uid": str}, "uid"), "uid", false, true},
		{obj(map[string]any{"ids": map[string]any{"type": []any{"null", "array"}, "items": str}}), "ids", true, true},
		{obj(map[string]any{"ids": strs, "to": str}, "ids", "to"), "", false, false},
		{obj(map[string]any{"version": str}), "", false, false},
		{obj(map[string]any{"ids": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}}}, "ids"), "", false, false},
	} {
		arg, list, ok := actArg(tc.schema)
		if arg != tc.arg || list != tc.list || ok != tc.ok {
			t.Errorf("%v: %q %v %v", tc.schema, arg, list, ok)
		}
	}
}

func mustRead(t *testing.T, c *core, id string) *nodestore.Doc {
	t.Helper()
	d, err := c.nodes.Read(id, 0)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
