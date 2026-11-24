package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// A call's time comes from its arguments that are a date or a date-time:
// those its schema declares, or, with no format said, those that are one
// whole. The earliest starts it, the latest ends it; never a name.
func TestCallSpan(t *testing.T) {
	loc := time.FixedZone("UTC+1", 3600)
	str := func(format string) map[string]any {
		s := map[string]any{"type": "string"}
		if format != "" {
			s["format"] = format
		}
		return s
	}
	obj := func(props map[string]any) map[string]any {
		return map[string]any{"type": "object", "properties": props}
	}
	at := func(s string) time.Time {
		v, ok := parseDated(s, loc)
		if !ok {
			t.Fatalf("not a time: %s", s)
		}
		return v.At
	}
	for _, tc := range []struct {
		name       string
		schema     map[string]any
		input      string
		ok, allDay bool
		start, end string
	}{
		{"declared", obj(map[string]any{"a": str("date-time"), "b": str("date-time")}),
			`{"b":"2026-11-20T15:00:00+01:00","a":"2026-11-20T14:00:00+01:00","title":"x"}`, true, false, "2026-11-20T14:00:00+01:00", "2026-11-20T15:00:00+01:00"},
		{"no schema, whole values, in the user's zone", nil,
			`{"x":"2026-11-20T09:30","y":"un texte du 2026-11-20T10:00","n":3}`, true, false, "2026-11-20T09:30:00+01:00", ""},
		{"a date is all day", obj(map[string]any{"d": str("date")}), `{"d":"2026-11-21"}`, true, true, "2026-11-21", ""},
		{"nested and in lists", obj(map[string]any{"slots": map[string]any{"type": "array", "items": obj(map[string]any{"at": str("date-time")})}}),
			`{"slots":[{"at":"2026-11-22T08:00:00Z"},{"at":"2026-11-22T07:00:00Z"}]}`, true, false, "2026-11-22T07:00:00Z", "2026-11-22T08:00:00Z"},
		{"another format is not a time", obj(map[string]any{"id": str("uuid")}), `{"id":"2026-11-20T09:30"}`, false, false, "", ""},
		{"an enum is not a time", obj(map[string]any{"v": map[string]any{"type": "string", "enum": []any{"2026-11-20"}}}), `{"v":"2026-11-20"}`, false, false, "", ""},
		{"declared date-time but a date", obj(map[string]any{"a": str("date-time")}), `{"a":"2026-11-20"}`, false, false, "", ""},
		{"nothing dated", nil, `{"to":"sam@example.org","body":"Bonjour"}`, false, false, "", ""},
	} {
		var input any
		json.Unmarshal([]byte(tc.input), &input)
		s, ok := spanOf(datedValues(tc.schema, input, loc))
		if ok != tc.ok {
			t.Errorf("%s: ok %v", tc.name, ok)
			continue
		}
		if !ok {
			continue
		}
		if s.AllDay != tc.allDay || !s.Start.Equal(at(tc.start)) || (tc.end == "") != s.End.IsZero() || (tc.end != "" && !s.End.Equal(at(tc.end))) {
			t.Errorf("%s: %+v", tc.name, s.view())
		}
	}
}

// heldCall keeps a call of a task's on a card, as a write held for the
// user's yes.
func heldCall(t *testing.T, c *core, input string) int64 {
	t.Helper()
	id := c.newAccessAsk(accessQuestion{TaskID: "t-agenda", Need: accessNeed{Server: "agenda", Category: catChange},
		Call: "mcp__agenda__add_entry []", Input: input})
	if id == 0 {
		t.Fatal("no access ask")
	}
	return id
}

// An Agenda of a node's page is the place of its dated cards: one that
// waits on a day the Agenda draws shows there, its Oui on its ghost, and
// not at the page's foot. A day it does not draw, an Agenda that shows no
// proposals, one whose date core cannot read, or a page without one keeps
// the card at the foot: a card that waits is never nowhere.
func TestAgendaPlacesDatedCards(t *testing.T) {
	c, _, _ := viewCore(t)
	tw := newTestWebFor(t, c)
	tw.setup()
	tomorrow := time.Now().In(c.userZone()).AddDate(0, 0, 1).Format(time.DateOnly)
	ask := heldCall(t, c, `{"title":"Relire le devis","start":"`+tomorrow+`T16:00","end":"`+tomorrow+`T17:00"}`)
	ref := proposalRef(cardAccess, ask)
	for _, tc := range []struct {
		agenda string // the Agenda's fields, none: no block
		foot   bool
	}{
		{"", true},
		{`"days": 2`, false},
		{`"days": 7`, false},
		{`"date": "` + tomorrow + `"`, false},
		{`"days": 1`, true},
		{`"days": 2, "proposals": false`, true},
		{`"date": {"@path": "/jour"}, "days": 7`, true},
	} {
		body := "Le jour.\n"
		if tc.agenda != "" {
			body += "\n```eriphe:block jour\n" + `{"components": [{"id": "root", "component": "Agenda", ` + tc.agenda + `, "events": []}], "data": {"jour": "` + tomorrow + `"}}` + "\n```\n"
		}
		id := mustNode(t, c, nodestore.New{Title: "Ma journée", Body: body})
		c.store.db.Exec(`INSERT INTO node_cards (node, ref, created) VALUES (?, ?, 1)`, id, ref)
		page, err := tw.w.nodePageOf(id, 0, "fr")
		if err != nil {
			t.Fatal(err)
		}
		if tc.agenda != "" && (len(page.Parts) < 2 || page.Parts[1].Block == nil || page.Parts[1].Block.Error != "") {
			t.Fatalf("%s: the block does not read: %+v", tc.agenda, page.Parts)
		}
		if got := slices.ContainsFunc(page.Cards, func(v proposalView) bool { return v.Ref == ref }); got != tc.foot {
			t.Errorf("Agenda {%s}: at the foot %v, want %v", tc.agenda, got, tc.foot)
		}
	}
}

// An Agenda's ghosts: the proposals that wait and take place that day, in the
// user's zone, an intention by its due date, a held call by its
// arguments; none once answered.
func TestAgendaProposals(t *testing.T) {
	tw, csrf := newMemoryWeb(t)
	c := tw.c
	loc := c.userZone()
	due := time.Date(2026, 11, 20, 14, 0, 0, 0, loc)
	in := mustIntend(t, c.mem, &Intention{Text: "Appeler le garage", DueAt: due, State: intentProposed, SourceKind: srcBrain, SourceRef: "msg:1", Trust: trustFirstParty})
	c.cardProposed(cardIntention, in.ID)
	undated := proposedRule(t, c, "facture", "")
	brainSays(c, "Je te propose ça ?")
	ask := heldCall(t, c, `{"title":"Dentiste","start":"2026-11-20T14:30","end":"2026-11-20T15:30"}`)

	day := func(d string) (int, dayProposals) {
		code, body, _ := tw.do("GET", "/api/proposals?day="+d, nil, nil)
		var v dayProposals
		json.Unmarshal([]byte(body), &v)
		return code, v
	}
	code, v := day("2026-11-20")
	if code != 200 || len(v.Proposals) != 2 {
		t.Fatalf("GET: %d %+v", code, v)
	}
	byRef := map[string]proposalView{}
	for _, p := range v.Proposals {
		byRef[p.Ref] = p
	}
	ip, ap := byRef[proposalRef(cardIntention, in.ID)], byRef[proposalRef(cardAccess, ask)]
	if len(ip.When) != 1 || ip.When[0].Start != due.Format(time.RFC3339) || ip.State != cardPending || ip.Card == 0 {
		t.Errorf("intention: %+v", ip)
	}
	if len(ap.When) != 1 || ap.When[0].Start != time.Date(2026, 11, 20, 14, 30, 0, 0, loc).Format(time.RFC3339) || ap.When[0].End == "" {
		t.Errorf("held call: %+v", ap)
	}
	if _, ok := byRef[proposalRef(cardRule, undated.ID)]; ok {
		t.Error("an undated proposal is a ghost")
	}
	if _, v := day("2026-11-21"); len(v.Proposals) != 0 {
		t.Errorf("another day: %+v", v)
	}
	if code, _ := day("demain"); code != http.StatusBadRequest {
		t.Errorf("no day: %d", code)
	}
	// Its own ref says when too.
	if _, body, _ := tw.do("GET", "/api/proposals/"+url.PathEscape(proposalRef(cardIntention, in.ID)), nil, nil); !strings.Contains(body, `"when":[{"start":"`+due.Format(time.RFC3339)) {
		t.Errorf("GET ref: %s", body)
	}

	// Answered, it is no longer a ghost.
	x := cardOf(t, c, cardIntention, in.ID)
	if code, _, _ := tw.do("POST", fmt.Sprintf("/cards/%d/yes", x.ID), url.Values{}, map[string]string{"X-CSRF-Token": csrf}); code != 200 {
		t.Fatalf("yes: %d", code)
	}
	if _, v := day("2026-11-20"); len(v.Proposals) != 1 || v.Proposals[0].Ref != proposalRef(cardAccess, ask) {
		t.Errorf("after yes: %+v", v)
	}
}

// Every way a proposal is decided says so to the workspace: a card's button, one found decided elsewhere; and a new card.
func TestProposalMoves(t *testing.T) {
	tw, _ := newMemoryWeb(t)
	c := tw.c
	_, seq, ch := c.watchProposals(0)
	r := proposedRule(t, c, "facture", "")
	brainSays(c, "Je ne te préviens plus des factures ?")
	ref := proposalRef(cardRule, r.ID)
	moved := func(what string) {
		t.Helper()
		select {
		case <-ch:
		default:
			t.Fatalf("%s: no move", what)
		}
		var ev proposalsEvent
		ev, seq, ch = c.watchProposals(seq)
		if !slices.Contains(ev.Refs, ref) || ev.All {
			t.Fatalf("%s: %+v", what, ev)
		}
	}
	moved("a new card")

	// Decided where no card is told (its own page): found when read.
	r2 := proposedRule(t, c, "banque", "")
	brainSays(c, "Et celle-là ?")
	ref = proposalRef(cardRule, r2.ID)
	moved("another card")
	if _, err := c.approveNotifyRule(r2.ID); err != nil {
		t.Fatal(err)
	}
	c.liveCard(cardOf(t, c, cardRule, r2.ID))
	moved("found decided")

	// A card linked to a node names it: its page reads it again.
	c.store.db.Exec(`INSERT INTO node_cards (node, ref, created) VALUES ('n1', ?, 1)`, ref)
	c.proposalMoved(ref)
	if ev, _, _ := c.watchProposals(seq); !slices.Contains(ev.Refs, ref) || !slices.Equal(ev.Nodes, []string{"n1"}) {
		t.Errorf("a node's card: %+v", ev)
	}

	// A stream far behind reads everything again.
	for i := 0; i < proposalFeedMax+2; i++ {
		c.proposalMoved("rule:1")
	}
	if ev, _, _ := c.watchProposals(seq); !ev.All {
		t.Errorf("behind: %+v", ev)
	}
}

// The workspace's stream sends the refs that moved, for the blocks that
// show them to read them again.
func TestProposalsEvent(t *testing.T) {
	tw, csrf := newMemoryWeb(t)
	c := tw.c
	r := proposedRule(t, c, "facture", "")
	brainSays(c, "Je ne te préviens plus des factures ?")
	ref := proposalRef(cardRule, r.ID)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, "GET", tw.srv.URL+"/events", nil)
	resp, err := tw.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	events := make(chan proposalsEvent, 10)
	ready := make(chan struct{})
	var snapshot sync.Once
	go func() {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(nil, 1<<20)
		event := ""
		for sc.Scan() {
			line := sc.Text()
			if v, ok := strings.CutPrefix(line, "event: "); ok {
				event = v
				if event == "node" {
					snapshot.Do(func() { close(ready) })
				}
			}
			if data, ok := strings.CutPrefix(line, "data: "); ok && event == "proposals" {
				var ev proposalsEvent
				if json.Unmarshal([]byte(data), &ev) == nil {
					events <- ev
				}
			}
		}
	}()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("no snapshot")
	}
	// At first, everything: what the page shows may have been decided
	// while it was away.
	select {
	case ev := <-events:
		if !ev.All {
			t.Fatalf("first event %+v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no first proposals event")
	}
	x := cardOf(t, c, cardRule, r.ID)
	if code, _, _ := tw.do("POST", fmt.Sprintf("/cards/%d/no", x.ID), url.Values{}, map[string]string{"X-CSRF-Token": csrf}); code != 200 {
		t.Fatalf("no: %d", code)
	}
	select {
	case ev := <-events:
		if !slices.Contains(ev.Refs, ref) {
			t.Fatalf("event %+v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no proposals event")
	}
}
