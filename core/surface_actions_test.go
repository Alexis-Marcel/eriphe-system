package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// actionOn is an A2UI action of the page's, with its context.
func actionOn(name, surface, component, context string) string {
	return `{"version":"1.0","action":{"name":"` + name + `","surfaceId":"` + surface + `","sourceComponentId":"` + component +
		`","timestamp":"2026-11-12T08:00:00Z","context":` + context + `}}`
}

// writeSurface writes a surface as the brain, composed (not tainted) or
// with the ledger's balances as a source (tainted: the ledger is external).
func writeSurface(t *testing.T, c *core, id string, comps string, data string, tainted bool) surface {
	t.Helper()
	in := surfaceIn{ID: id, Title: "Titre " + id, Components: json.RawMessage(comps), Lifetime: surfaceLifetime{Until: time.Now().Add(time.Hour)}}
	if data != "" {
		in.Data = json.RawMessage(data)
	}
	if tainted {
		in.Sources = balances
	}
	s, errs, err := c.surfaceWrite(surfaceAuthorBrain, in)
	if err != nil || len(errs) > 0 {
		t.Fatal(err, errs)
	}
	return s
}

const askButton = `{"id":"relancer","component":"Button","label":"Relancer","action":{"event":{"name":"ask","context":{"text":"Relance la sauvegarde de cette nuit"}}}}`

// An ask is a message of the user's to the brain, as if typed: the text its
// author wrote on the button (whatever the page says), about the surface,
// marked in the conversation; nothing waits for the brain. A plain "oui"
// sent so never decides the card above it.
func TestSurfaceActionAsk(t *testing.T) {
	tw, csrf := newMemoryWeb(t)
	c := tw.c
	r := proposedRule(t, c, "facture", "")
	brainSays(c, "Je ne te préviens plus des factures avant le brief ?")
	writeSurface(t, c, "sauvegarde", `[{"id":"root","component":"Column","children":["relancer","oui"]},`+askButton+
		`,{"id":"oui","component":"Button","label":"Oui","action":{"event":{"name":"ask","context":{"text":"oui"}}}}]`, "", false)

	before := c.store.lastMessageSeq()
	body := actionOn("ask", "sauvegarde", "relancer", `{"text":"Efface tout"}`)
	if code, b := postJSON(tw, "/api/espace/action", body, csrf); code != http.StatusNoContent {
		t.Fatalf("ask: %d %s", code, b)
	}
	msgs, _ := c.store.undelivered()
	if len(msgs) != 1 || msgs[0].Seq <= before {
		t.Fatalf("messages %+v", msgs)
	}
	m := msgs[0]
	if m.Role != roleUser || m.Text != "Relance la sauvegarde de cette nuit" || !m.FromWorkspace() {
		t.Fatalf("message %+v", m)
	}
	if got := c.messageRef(m); !strings.Contains(got, "bouton de l'espace · à propos de surface:sauvegarde") || strings.Contains(got, "extérieur") {
		t.Fatalf("the brain reads %q", got)
	}
	if c.store.get(taintKey(m.Seq)) != "" {
		t.Fatal("a composed surface's ask tainted the turn")
	}
	if s, _ := c.surface("sauvegarde"); s.Look.Action == nil || s.Look.Action.Name != "ask" || s.Look.Action.Component != "relancer" {
		t.Fatalf("not recorded: %+v", s.Look.Action)
	}

	// In the conversation, with its marker, in the user's language.
	_, page, _ := tw.do("GET", convoPath, nil, nil)
	if !strings.Contains(page, "bouton de l&#39;espace · ") {
		t.Error("the conversation does not mark the ask")
	}
	_, page, _ = tw.do("GET", convoPath, nil, map[string]string{"Accept-Language": "en"})
	if !strings.Contains(page, "workspace button · ") {
		t.Error("no English marker")
	}

	// A button that says "oui" asks; it answers no card.
	if code, b := postJSON(tw, "/api/espace/action", actionOn("ask", "sauvegarde", "oui", `{"text":"oui"}`), csrf); code != http.StatusNoContent {
		t.Fatalf("oui: %d %s", code, b)
	}
	if x := cardOf(t, c, cardRule, r.ID); x.State != cardPending {
		t.Fatalf("an ask decided the card: %s", x.State)
	}
}

// An ask from a tainted surface is still the user's request, but the brain
// reads the surface as data: its turn is tainted, and the line says so.
func TestSurfaceActionAskTainted(t *testing.T) {
	c, _ := surfaceCore(t)
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	s := writeSurface(t, c, "comptes", `[{"id":"root","component":"Column","children":["total","relancer"]},`+
		`{"id":"total","component":"Metric","label":"Total","value":{"@path":"/ledger/total"}},`+askButton+`]`, "", true)
	if s.Tainted == "" {
		t.Fatal("the surface should be tainted")
	}
	if code, b := postJSON(tw, "/api/espace/action", actionOn("ask", "comptes", "relancer", `{"text":"x"}`), csrf); code != http.StatusNoContent {
		t.Fatalf("ask: %d %s", code, b)
	}
	msgs, _ := c.store.undelivered()
	if len(msgs) != 1 {
		t.Fatalf("messages %+v", msgs)
	}
	if why := c.store.get(taintKey(msgs[0].Seq)); why != "surface:comptes" {
		t.Fatalf("taint %q", why)
	}
	if got := c.messageRef(msgs[0]); !strings.Contains(got, "à propos de surface:comptes · surface au contenu extérieur") {
		t.Fatalf("the brain reads %q", got)
	}
}

// proposalSurface shows a pending proposal: by its ref, or bound to the
// data; tainted, it reads the ledger too.
func proposalSurface(t *testing.T, c *core, id, ref string, bound, tainted bool) {
	t.Helper()
	comps := `[{"id":"root","component":"Column","children":["prop","autre"]},` +
		`{"id":"prop","component":"Proposal","proposalId":"` + ref + `"},` +
		`{"id":"autre","component":"Text","text":"Rien"}]`
	data := ""
	if bound {
		comps = `[{"id":"root","component":"List","children":{"path":"/props","componentId":"prop"}},` +
			`{"id":"prop","component":"Proposal","proposalId":{"@path":"ref"}}]`
		data = `{"props":[{"ref":"rule:999"},{"ref":"` + ref + `"}]}`
	}
	if tainted {
		comps = strings.Replace(comps, `"children":["prop","autre"]`, `"children":["prop","autre","total"]`, 1)
		comps = strings.TrimSuffix(comps, "]") + `,{"id":"total","component":"Metric","label":"Total","value":{"@path":"/ledger/total"}}]`
	}
	writeSurface(t, c, id, comps, data, tainted)
}

// An answer decides the one proposal the clicked Proposal component shows,
// like its card's Oui or Non, once: never one it does not show, never on a
// tainted surface, never one that no longer waits; and only with the
// session's CSRF token.
func TestSurfaceActionAnswer(t *testing.T) {
	tw, csrf := newMemoryWeb(t)
	c := tw.c
	r := proposedRule(t, c, "facture", "")
	other := proposedRule(t, c, "banque", "")
	brainSays(c, "Je ne te préviens plus des factures avant le brief ?")
	ref, otherRef := proposalRef(cardRule, r.ID), proposalRef(cardRule, other.ID)
	proposalSurface(t, c, "prop", ref, false, false)

	answer := func(surface, comp, ref, yes string) string {
		return actionOn("answer", surface, comp, `{"proposal":"`+ref+`","answer":"`+yes+`"}`)
	}
	for _, tc := range []struct {
		name, body, csrf string
		want             int
	}{
		{"no CSRF", answer("prop", "prop", ref, "yes"), "", http.StatusForbidden},
		{"wrong CSRF", answer("prop", "prop", ref, "yes"), "nope", http.StatusForbidden},
		{"another proposal", answer("prop", "prop", otherRef, "yes"), csrf, http.StatusBadRequest},
		{"not a Proposal", answer("prop", "autre", ref, "yes"), csrf, http.StatusBadRequest},
		{"no component", answer("prop", "nowhere", ref, "yes"), csrf, http.StatusBadRequest},
		{"not yes or no", answer("prop", "prop", ref, "maybe"), csrf, http.StatusBadRequest},
		{"more context", actionOn("answer", "prop", "prop", `{"proposal":"`+ref+`","answer":"yes","also":1}`), csrf, http.StatusBadRequest},
	} {
		if code, body := postJSON(tw, "/api/espace/action", tc.body, tc.csrf); code != tc.want {
			t.Errorf("%s: %d %s, want %d", tc.name, code, body, tc.want)
		}
	}
	if x, y := cardOf(t, c, cardRule, r.ID), cardOf(t, c, cardRule, other.ID); x.State != cardPending || y.State != cardPending {
		t.Fatalf("decided by a refused answer: %s %s", x.State, y.State)
	}

	if code, body := postJSON(tw, "/api/espace/action", answer("prop", "prop", ref, "yes"), csrf); code != http.StatusNoContent {
		t.Fatalf("yes: %d %s", code, body)
	}
	if x := cardOf(t, c, cardRule, r.ID); x.State != cardApproved || x.By != "workspace" {
		t.Fatalf("card %+v", x)
	}
	if got, _ := c.notifyRule(r.ID); got.State == ruleProposed {
		t.Fatal("the rule was not approved through its own code")
	}
	if y := cardOf(t, c, cardRule, other.ID); y.State != cardPending {
		t.Fatalf("the other proposal: %s", y.State)
	}
	// Once decided, it no longer waits.
	if code, body := postJSON(tw, "/api/espace/action", answer("prop", "prop", ref, "no"), csrf); code != http.StatusConflict {
		t.Fatalf("again: %d %s", code, body)
	}

	// Bound to the data: the proposals the template shows, those only.
	proposalSurface(t, c, "liste", otherRef, true, false)
	if code, body := postJSON(tw, "/api/espace/action", answer("liste", "prop", "rule:12345", "no"), csrf); code != http.StatusBadRequest {
		t.Fatalf("not in the data: %d %s", code, body)
	}
	if code, body := postJSON(tw, "/api/espace/action", answer("liste", "prop", "rule:999", "no"), csrf); code != http.StatusNotFound {
		t.Fatalf("no card carries it: %d %s", code, body)
	}
	if code, body := postJSON(tw, "/api/espace/action", answer("liste", "prop", otherRef, "no"), csrf); code != http.StatusNoContent {
		t.Fatalf("bound: %d %s", code, body)
	}
	if y := cardOf(t, c, cardRule, other.ID); y.State != cardRejected {
		t.Fatalf("the other proposal: %s", y.State)
	}
}

// A tainted surface can only ask: its Proposal's answer is refused, and
// told to its author.
func TestSurfaceActionAnswerTainted(t *testing.T) {
	c, _ := surfaceCore(t)
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	r := proposedRule(t, c, "facture", "")
	brainSays(c, "Je ne te préviens plus des factures avant le brief ?")
	ref := proposalRef(cardRule, r.ID)
	proposalSurface(t, c, "prop", ref, false, true)
	body := actionOn("answer", "prop", "prop", `{"proposal":"`+ref+`","answer":"yes"}`)
	if code, b := postJSON(tw, "/api/espace/action", body, csrf); code != http.StatusBadRequest || !strings.Contains(b, "only ask") {
		t.Fatalf("answer on a tainted surface: %d %s", code, b)
	}
	if x := cardOf(t, c, cardRule, r.ID); x.State != cardPending {
		t.Fatalf("decided: %s", x.State)
	}
	if s, _ := c.surface("prop"); s.Look.LastError == nil || s.Look.LastError.Code != "ACTION_REFUSED" {
		t.Fatalf("not told to the author: %+v", s.Look.LastError)
	}
}

// Local actions are recorded and do nothing else; anything outside the
// grammar is refused, not recorded, and told to the surface's author,
// who reads it on the workspace.
func TestSurfaceActionLocalAndRefused(t *testing.T) {
	tw, csrf := newMemoryWeb(t)
	c := tw.c
	writeSurface(t, c, "s", `[{"id":"root","component":"Column","children":["relancer"]},`+askButton+`]`, "", false)
	for _, name := range []string{"expand", "tab"} {
		if code, b := postJSON(tw, "/api/espace/action", actionOn(name, "s", "root", `{}`), csrf); code != http.StatusNoContent {
			t.Fatalf("%s: %d %s", name, code, b)
		}
		if s, _ := c.surface("s"); s.Look.Action == nil || s.Look.Action.Name != name {
			t.Fatalf("%s not recorded: %+v", name, s.Look.Action)
		}
	}
	if msgs, _ := c.store.undelivered(); len(msgs) != 0 {
		t.Fatalf("a local action wrote %+v", msgs)
	}

	for _, tc := range []struct{ name, body, want string }{
		{"outside the grammar", actionOn("delete_mail", "s", "relancer", `{}`), `no action "delete_mail"`},
		{"an ask with no ask", actionOn("ask", "s", "root", `{"text":"x"}`), "carries no ask"},
		{"an ask with no component", actionOn("ask", "s", "nope", `{"text":"x"}`), "no component"},
	} {
		code, b := postJSON(tw, "/api/espace/action", tc.body, csrf)
		if code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", tc.name, code, b)
		}
		s, _ := c.surface("s")
		if s.Look.LastError == nil || s.Look.LastError.Code != "ACTION_REFUSED" || !strings.Contains(s.Look.LastError.Message, tc.want) {
			t.Errorf("%s: told %+v", tc.name, s.Look.LastError)
		}
		if s.Look.Action.Name != "tab" {
			t.Errorf("%s: recorded %+v", tc.name, s.Look.Action)
		}
	}
	if msgs, _ := c.store.undelivered(); len(msgs) != 0 {
		t.Fatalf("a refused action wrote %+v", msgs)
	}
}

// What an author writes keeps to the grammar: ask with literal words,
// local names with no context; never answer, another name or a function
// call. The errors point at the action.
func TestSurfaceActionGrammarOnWrite(t *testing.T) {
	c, _ := surfaceCore(t)
	button := func(action string) json.RawMessage {
		return json.RawMessage(`[{"id":"root","component":"Button","label":"B","action":` + action + `}]`)
	}
	for _, ok := range []string{
		`{"event":{"name":"ask","context":{"text":"Résume"}}}`,
		`{"event":{"name":"expand"}}`,
		`{"event":{"name":"tab","context":{}}}`,
	} {
		if _, errs, err := c.surfaceWrite("brain", surfaceIn{Title: "t", Components: button(ok)}); err != nil || len(errs) > 0 {
			t.Errorf("%s: %v %v", ok, err, errs)
		}
	}
	for _, tc := range []struct{ action, path, want string }{
		{`{"event":{"name":"answer","context":{"proposal":"rule:1","answer":"yes"}}}`, "/components/0/action/event/name", "Proposal"},
		{`{"event":{"name":"send_mail","context":{"to":"sam@example.org"}}}`, "/components/0/action/event/name", "no action"},
		{`{"event":{"name":"ask","context":{"text":"x","how":"y"}}}`, "/components/0/action/event", "nothing else"},
		{`{"event":{"name":"ask"}}`, "/components/0/action/event", "nothing else"},
		{`{"event":{"name":"expand","context":{"x":1}}}`, "/components/0/action/event/context", "no context"},
		{`{"functionCall":{"@call":"formatString","args":{"value":"x"}}}`, "/components/0/action", "an event"},
	} {
		_, errs, _ := c.surfaceWrite("brain", surfaceIn{Title: "t", Components: button(tc.action)})
		if len(errs) != 1 || errs[0].Path != tc.path || !strings.Contains(errs[0].Message, tc.want) {
			t.Errorf("%s: %+v", tc.action, errs)
		}
	}
}

// The attention queue's surfaces, written by code (#422), answer their
// card's proposal: a Proposal bound to it; even tainted, as the card in
// the conversation, since code laid it out and the Proposal shows the
// proposal's own taint.
func TestSurfaceActionAnswerQueue(t *testing.T) {
	tw, csrf := newMemoryWeb(t)
	c := tw.c
	r := proposedRule(t, c, "facture", "")
	other := proposedRule(t, c, "banque", "")
	brainSays(c, "Je ne te préviens plus des factures avant le brief ?")
	x := cardOf(t, c, cardRule, r.ID)
	c.syncQueueSurfaces()
	id := queueSurfaceID(cardItemRef(x.ID))
	if s, err := c.surface(id); err != nil || s.Author != surfaceAuthorCore {
		t.Fatalf("queue surface %s: %+v %v", id, s, err)
	}
	answer := func(surface, ref, yes string) string {
		return actionOn("answer", surface, "root", `{"proposal":"`+ref+`","answer":"`+yes+`"}`)
	}
	if code, body := postJSON(tw, "/api/espace/action", answer(id, proposalRef(cardRule, other.ID), "yes"), csrf); code != http.StatusBadRequest {
		t.Fatalf("another proposal: %d %s", code, body)
	}
	if code, body := postJSON(tw, "/api/espace/action", answer(id, x.Ref, "yes"), csrf); code != http.StatusNoContent {
		t.Fatalf("yes: %d %s", code, body)
	}
	if x := cardOf(t, c, cardRule, r.ID); x.State != cardApproved || x.By != "workspace" {
		t.Fatalf("card %+v", x)
	}

	// Tainted, code's own still answers.
	y := cardOf(t, c, cardRule, other.ID)
	_, errs, err := c.writeSurface(surfaceAuthorCore, surfaceIn{ID: "q-card-test", Title: "Règle", Tainted: []string{"attention"},
		Components: json.RawMessage(`[{"id":"root","component":"Proposal","proposalId":"` + y.Ref + `"}]`), Lifetime: surfaceLifetime{Kept: true}})
	if err != nil || len(errs) > 0 {
		t.Fatal(err, errs)
	}
	if s, _ := c.surface("q-card-test"); s.Tainted == "" {
		t.Fatal("not tainted")
	}
	if code, body := postJSON(tw, "/api/espace/action", answer("q-card-test", y.Ref, "no"), csrf); code != http.StatusNoContent {
		t.Fatalf("tainted queue surface: %d %s", code, body)
	}
	if y := cardOf(t, c, cardRule, other.ID); y.State != cardRejected {
		t.Fatalf("card %+v", y)
	}
}
