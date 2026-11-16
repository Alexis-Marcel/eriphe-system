package main

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
)

// rowAt is where an item's row is in a thread's surface data, and the row.
func rowAt(t *testing.T, c *core, thread int64, item string) (string, map[string]any) {
	t.Helper()
	_, data, _ := threadSurfaceOf(t, c, thread)
	srcs, _ := data["sources"].([]any)
	for si, s := range srcs {
		groups, _ := s.(map[string]any)["groups"].([]any)
		for gi, g := range groups {
			items, _ := g.(map[string]any)["items"].([]any)
			for ii, it := range items {
				if row := it.(map[string]any); row["id"] == item {
					return "/" + threadDataKey + "/sources/" + strconv.Itoa(si) + "/groups/" + strconv.Itoa(gi) + "/items/" + strconv.Itoa(ii), row
				}
			}
		}
	}
	t.Fatalf("no row %q in %v", item, data["sources"])
	return "", nil
}

// rowCardRefs are the refs of the cards on a row.
func rowCardRefs(row map[string]any) []string {
	var out []string
	cards, _ := row["cards"].([]any)
	for _, x := range cards {
		out = append(out, x.(map[string]any)["ref"].(string))
	}
	return out
}

// answerAt answers a card of a thread's surface as its Proposal does.
func answerAt(c *core, thread int64, component, ref, at string, yes bool) error {
	ctx := map[string]json.RawMessage{"proposal": json.RawMessage(strconv.Quote(ref)), "answer": json.RawMessage(`"no"`)}
	if yes {
		ctx["answer"] = json.RawMessage(`"yes"`)
	}
	if at != "" {
		ctx["at"] = json.RawMessage(strconv.Quote(at))
	}
	return c.surfaceAction(context.Background(), threadSurfaceID(thread), a2ui.Action{Name: a2ui.ActionAnswer, SurfaceID: threadSurfaceID(thread),
		SourceComponentID: component, Context: ctx})
}

// permission is a task's request to call a tool, as its events say it.
func permission(pid, tool string, input map[string]any) EventRow {
	return EventRow{Seq: 1, Time: time.Now(), Type: "permission", Data: map[string]any{"id": pid, "tool_name": tool, "input": input}}
}

// A watcher's run that proposes an action on three of its thread's items
// gets three cards, each bound to its item and shown on its row; its
// call on no item stays on the run's card. Oui on one row runs only that
// call; another row's card answered from this row, or a row's card with
// no row said, is refused. The same call on the same item kept again is
// the card already there.
func TestRunCallsSplitByItem(t *testing.T) {
	c, fm, now := alertsWatcher(t)
	fm.mu.Lock()
	fm.rules = append(fm.rules, map[string]any{"uid": "b", "name": "down"}, map[string]any{"uid": "c", "name": "certs"}, map[string]any{"uid": "d", "name": "disk"})
	fm.mu.Unlock()
	lookAt(c, now.Add(time.Minute))
	lookAt(c, now.Add(2*time.Minute))
	c.tickSkills(context.Background(), now.Add(2*time.Minute))
	rs := runsOfSkill(t, c, "veille-alertes")
	if len(rs) != 1 {
		t.Fatalf("runs %+v", rs)
	}
	r := rs[0]
	id := runThreadOf(c, r)
	c.surf.refreshing.Wait()
	c.readThreadSources(context.Background(), id, nil, time.Now())

	for i, uid := range []string{"b", "c", "d"} {
		ev := []EventRow{permission("s"+strconv.Itoa(i), "mcp__monitor__silence", map[string]any{"uid": uid})}
		c.store.addEvents(r.Task, ev)
		c.applyPolicy(r.Task, ev)
	}
	ev := []EventRow{permission("rb", "mcp__monitor__rollback", map[string]any{"version": "2.0.0"})}
	c.store.addEvents(r.Task, ev)
	c.applyPolicy(r.Task, ev)
	if _, ok := runCard(c, cardCalls); ok {
		t.Fatal("an item's card before the run's end")
	}
	r = endTurn(t, c, r.Task, "Trois alertes à faire taire, et la 2.0.0 à remettre.")

	byItem := map[string]proposal{}
	for _, p := range c.pendingProposals() {
		if p.Kind != cardCalls {
			continue
		}
		asks := c.batchAsks(p.ID)
		if len(asks) != 1 || asks[0].Thread != id || asks[0].Source != "alertes" || asks[0].Run != r.ID || asks[0].Tainted == "" {
			t.Fatalf("card %+v asks %+v", p, asks)
		}
		byItem[asks[0].Item] = p
	}
	if len(byItem) != 3 || byItem["b"].ID == 0 || byItem["c"].ID == 0 || byItem["d"].ID == 0 {
		t.Fatalf("cards by item %+v", byItem)
	}
	p := byItem["b"]
	if o := p.Origin("fr"); o.Thread != id || !strings.Contains(o.From, "b") || !strings.Contains(o.From, "alertes") || p.Tainted == "" {
		t.Fatalf("origin %+v tainted %q", o, p.Tainted)
	}
	run, ok := runCard(c, cardRunCalls)
	if !ok || len(run.Fields) != 1 || !strings.Contains(run.Fields[0].Value, "2.0.0") {
		t.Fatalf("the run's card %+v", run)
	}

	// Each card on its row, not with what waits for the user.
	c.keepThreadSurface(id)
	refs := map[string]string{}
	paths := map[string]string{}
	for _, uid := range []string{"b", "c", "d"} {
		at, row := rowAt(t, c, id, uid)
		got := rowCardRefs(row)
		if len(got) != 1 || got[0] != proposalRef(cardCalls, byItem[uid].ID) {
			t.Fatalf("row %s cards %v", uid, got)
		}
		refs[uid], paths[uid] = got[0], at
	}
	if _, row := rowAt(t, c, id, "a"); len(rowCardRefs(row)) != 0 {
		t.Fatalf("row a %v", row)
	}
	s, _, _ := threadSurfaceOf(t, c, id)
	if strings.Contains(string(s.Components), `"proposalId":"`+refs["b"]+`"`) {
		t.Fatal("the item's card shows with what waits for the user too")
	}
	if c.threadShownCards()[refs["b"]] != id {
		t.Fatal("the thread does not answer its item's card: it stays in the queue")
	}

	// Another row's card, or no row: refused; nothing runs.
	var refused *errActionRefused
	if err := answerAt(c, id, "thread_src_0_card", refs["c"], paths["b"]+"/cards/0", true); !errors.As(err, &refused) {
		t.Fatalf("row c's card from row b: %v", err)
	}
	if err := answerAt(c, id, "thread_src_0_card", refs["b"], "", true); !errors.As(err, &refused) {
		t.Fatalf("no row: %v", err)
	}
	if err := answerAt(c, id, "thread_src_0_card", refs["b"], "/thread/sources/0/cards/0", true); !errors.As(err, &refused) {
		t.Fatalf("a place with no card: %v", err)
	}
	// Its own row: only that call runs.
	if err := answerAt(c, id, "thread_src_0_card", refs["b"], paths["b"]+"/cards/0", true); err != nil {
		t.Fatal(err)
	}
	waitNote(t, c, r.ID, "silence")
	fm.mu.Lock()
	silenced := slices.Clone(fm.silenced)
	fm.mu.Unlock()
	if !slices.Equal(silenced, []string{"b"}) {
		t.Fatalf("silenced %v", silenced)
	}
	for _, uid := range []string{"c", "d"} {
		if c.cardState(refs[uid]) != cardPending {
			t.Errorf("card %s: %s", uid, c.cardState(refs[uid]))
		}
	}
	if _, ok := runCard(c, cardRunCalls); !ok {
		t.Error("the run's own card was answered too")
	}

	// The same call on c kept again: the card already there.
	task, _ := c.store.task(r.Task)
	var before int
	c.store.db.QueryRow(`SELECT COUNT(*) FROM access_asks`).Scan(&before)
	c.askRunCall(task, r, "again", "mcp__monitor__silence", map[string]any{"uid": "c"})
	var after int
	c.store.db.QueryRow(`SELECT COUNT(*) FROM access_asks`).Scan(&after)
	if after != before {
		t.Fatalf("asks %d then %d", before, after)
	}
}

// A call that names two items, or none its thread lists, or an item of
// another server's source, is on no item.
func TestCallItemMatch(t *testing.T) {
	c, fm, now := alertsWatcher(t)
	fm.mu.Lock()
	fm.rules = append(fm.rules, map[string]any{"uid": "b", "name": "down"})
	fm.mu.Unlock()
	lookAt(c, now.Add(time.Minute))
	lookAt(c, now.Add(2*time.Minute))
	c.tickSkills(context.Background(), now.Add(2*time.Minute))
	id := runThreadOf(c, runsOfSkill(t, c, "veille-alertes")[0])
	c.surf.refreshing.Wait()
	c.readThreadSources(context.Background(), id, nil, time.Now())
	for _, tc := range []struct {
		tool  string
		input map[string]any
		item  string
	}{
		{"mcp__monitor__silence", map[string]any{"uid": "b"}, "b"},
		{"mcp__monitor__silence", map[string]any{"uids": []any{"a", "b"}}, ""},
		{"mcp__monitor__silence", map[string]any{"uid": "z"}, ""},
		{"mcp__other__silence", map[string]any{"uid": "b"}, ""},
	} {
		_, item, ok := c.callItem(id, tc.tool, tc.input)
		if item != tc.item || ok != (tc.item != "") {
			t.Errorf("%s %v: %q %v", tc.tool, tc.input, item, ok)
		}
	}
}
