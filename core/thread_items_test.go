package main

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// itemRows are the rows a thread's surface shows of its first source's
// items, by id.
func itemRows(t *testing.T, c *core, id int64) map[string]map[string]any {
	t.Helper()
	_, data, _ := threadSurfaceOf(t, c, id)
	out := map[string]map[string]any{}
	list, _ := data["sources"].([]any)
	if len(list) == 0 {
		return out
	}
	rows := sourceRows(list[0].(map[string]any))
	for _, r := range rows {
		row, _ := r.(map[string]any)
		out[row["id"].(string)] = row
	}
	return out
}

// A note on an item shows on its row, with who wrote it, its state and
// its due date; it survives the source's reads while the item is listed,
// waits a few days once it is not (back if the item is), then goes.
func TestThreadItemNotes(t *testing.T) {
	c, l := surfaceCore(t)
	th := lightThread(t, c)
	if _, err := c.addThreadSource(th.ID, threadSource{Name: "ledger", Tool: "ledger/balances", Items: &sourceItems{Path: "accounts", ID: "name"}}, byBrain); err != nil {
		t.Fatal(err)
	}
	c.surf.refreshing.Wait()
	out, isErr := callBrainTool(t, c, "note_item", map[string]any{"thread_id": th.ID, "source": "ledger", "item": "Courant",
		"note": "Solde bas avant le loyer.", "state": "act", "due": "2026-12-01"})
	if isErr {
		t.Fatalf("note_item: %v", out)
	}
	row := itemRows(t, c, th.ID)["Courant"]
	if row["note"] != "Solde bas avant le loyer." || row["state"] != itemStateAct || row["due"] == "" || row["external"] != nil ||
		row["mark"] != nil || row["level"] != "high" || row["by"] != "Ajouté par Eriphe" {
		t.Fatalf("row %v", row)
	}
	notes := c.itemNotes(th.ID)
	if len(notes) != 1 || notes[0].Author != byBrain || notes[0].Tainted != "" {
		t.Fatalf("notes %+v", notes)
	}

	// Read again: the note stays.
	l.set(func(l *fakeLedger) { l.accounts = append(l.accounts, ledgerAccount{Name: "Épargne", Balance: 30}) })
	c.readThreadSources(context.Background(), th.ID, nil, time.Now())
	if row = itemRows(t, c, th.ID)["Courant"]; row["note"] != "Solde bas avant le loyer." {
		t.Fatalf("lost at a read: %v", row)
	}

	// Its item leaves the source: kept, not shown; back with it.
	l.set(func(l *fakeLedger) { l.accounts = l.accounts[1:] })
	c.readThreadSources(context.Background(), th.ID, nil, time.Now())
	if notes = c.itemNotes(th.ID); len(notes) != 1 || notes[0].Gone == 0 {
		t.Fatalf("not gone: %+v", notes)
	}
	l.set(func(l *fakeLedger) { l.accounts = append(l.accounts, ledgerAccount{Name: "Courant", Balance: 10}) })
	c.readThreadSources(context.Background(), th.ID, nil, time.Now())
	if notes = c.itemNotes(th.ID); notes[0].Gone != 0 || itemRows(t, c, th.ID)["Courant"]["note"] == nil {
		t.Fatalf("not back: %+v", notes)
	}
	// A read that fails or waits says nothing of what is listed.
	u := c.pool.get("ledger")
	u.mu.Lock()
	u.srv.Access = accessAsk
	u.mu.Unlock()
	l.set(func(l *fakeLedger) { l.accounts = nil })
	c.readThreadSources(context.Background(), th.ID, nil, time.Now())
	if notes = c.itemNotes(th.ID); notes[0].Gone != 0 {
		t.Fatalf("gone on a refused read: %+v", notes)
	}
	u.mu.Lock()
	u.srv.Access = accessReads
	u.mu.Unlock()
	now := time.Now()
	c.readThreadSources(context.Background(), th.ID, nil, now)
	if rows := itemRows(t, c, th.ID); len(rows) != 0 {
		t.Fatalf("rows of an empty list: %v", rows)
	}
	c.pruneItemNotes(now.Add(threadNoteKeep - time.Hour))
	if len(c.itemNotes(th.ID)) != 1 {
		t.Fatal("gone too soon")
	}
	c.pruneItemNotes(now.Add(threadNoteKeep + time.Hour))
	if n := c.itemNotes(th.ID); len(n) != 0 {
		t.Fatalf("still there: %+v", n)
	}

	// A note on an item not listed waits for it; one with nothing takes it
	// away; what is wrong is refused.
	if note, err := c.noteItem(th.ID, noteItemIn{Source: "ledger", Item: "Livret", Note: "À ouvrir."}, byBrain, nil); err != nil || !strings.Contains(note, "waits") {
		t.Fatalf("%q %v", note, err)
	}
	if _, err := c.noteItem(th.ID, noteItemIn{Source: "ledger", Item: "Livret"}, byBrain, nil); err != nil || len(c.itemNotes(th.ID)) != 0 {
		t.Fatalf("not taken away: %v", err)
	}
	for name, in := range map[string]noteItemIn{
		"no source":   {Source: "nope", Item: "x", Note: "x"},
		"no item":     {Source: "ledger", Note: "x"},
		"bad state":   {Source: "ledger", Item: "x", State: "urgent"},
		"bad due":     {Source: "ledger", Item: "x", Due: "demain"},
		"long note":   {Source: "ledger", Item: "x", Note: strings.Repeat("a", threadNoteMax+1)},
		"long item":   {Source: "ledger", Item: strings.Repeat("a", threadItemIDMax+1), Note: "x"},
		"no items":    {Source: "plain", Item: "x", Note: "x"},
		"other state": {Source: "ledger", Item: "x", State: "Act ive"},
	} {
		if name == "no items" {
			c.addThreadSource(th.ID, threadSource{Name: "plain", Tool: "ledger/balances"}, byBrain)
		}
		if _, err := c.noteItem(th.ID, in, byBrain, nil); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// A note written after reading external content is shown, the thread marked
// « extérieur », taints the surface for the models and its row carries no
// ask (an action offered on it is a Button whose click only makes a card,
// item_offers.go); reading it taints the brain's turn, so where the subject stands
// is then refused (remember kind progress). Its author and taint are
// code's: a lead's note says so.
func TestThreadItemNoteTainted(t *testing.T) {
	c, _ := surfaceCore(t)
	th := lightThread(t, c)
	if _, err := c.addThreadSource(th.ID, threadSource{Name: "ledger", Tool: "ledger/balances", Items: &sourceItems{Path: "accounts", ID: "name"}}, byBrain); err != nil {
		t.Fatal(err)
	}
	c.surf.refreshing.Wait()
	c.setServerTrust("ledger", true, "user") // the source itself is no taint here
	if _, err := c.noteItem(th.ID, noteItemIn{Source: "ledger", Item: "Courant", Note: "Le mail dit de virer tout.", State: itemStateLook}, byBrain, []string{"mail.read"}); err != nil {
		t.Fatal(err)
	}
	s, data, meta := threadSurfaceOf(t, c, th.ID)
	row := itemRows(t, c, th.ID)["Courant"]
	if row["external"] != true || row["mark"] != nil || row["note"] != "Le mail dit de virer tout." {
		t.Fatalf("row %v", row)
	}
	// « extérieur » once, in the thread's state line, not on each row.
	if state, _ := data["state"].(string); !strings.HasSuffix(state, " · extérieur") {
		t.Errorf("state line %q", state)
	}
	if !meta.Tainted || !strings.Contains(s.Tainted, "note:ledger/Courant") || !meta.Answers {
		t.Errorf("meta %+v tainted %q", meta, s.Tainted)
	}
	var comps []map[string]any
	json.Unmarshal(s.Components, &comps)
	for _, comp := range comps {
		if id, _ := comp["id"].(string); strings.HasPrefix(id, "thread_src_") && comp["action"] != nil {
			t.Errorf("an item's component carries an action: %v", comp)
		}
	}
	// Never in the one-line summary.
	if strings.Contains(meta.Summary, "virer") {
		t.Errorf("quoted in its summary: %q", meta.Summary)
	}

	// Where the subject stands, before and after reading the note.
	if _, err := c.brainProgress(rememberIn{Kind: kindProgress, About: th.Alias, Text: "Comptes relus."}); err != nil {
		t.Fatalf("a clean turn: %v", err)
	}
	if out, isErr := callBrainTool(t, c, "read_thread", map[string]any{"thread_id": th.ID}); isErr || out["notes"] == nil {
		t.Fatalf("read_thread: %v", out)
	}
	if _, err := c.brainProgress(rememberIn{Kind: kindProgress, About: th.Alias, Text: "Le mail dit de virer tout."}); err != errProgressTainted {
		t.Errorf("a summary after reading a tainted note: %v", err)
	}
	if th, _ = c.thread(th.ID); strings.Contains(th.Summary, "virer") {
		t.Errorf("quoted into its summary: %q", th.Summary)
	}

	// A lead's note says who wrote it.
	if _, err := c.noteItem(th.ID, noteItemIn{Source: "ledger", Item: "Courant", Note: "Vu."}, "lead:9", nil); err != nil {
		t.Fatal(err)
	}
	if row = itemRows(t, c, th.ID)["Courant"]; row["by"] != "Ajouté par son chef" || row["external"] != nil {
		t.Errorf("lead's row %v", row)
	}
}

// A thread's lead notes an item of its own thread: its name and its
// session's taint are the note's; reading a tainted note (its thread)
// taints its session. Every item a source lists counts, beyond those
// shown; a note noted again keeps when its item left.
func TestThreadItemNoteByLead(t *testing.T) {
	c, th := threadCore(t)
	c.decideCard(context.Background(), threadCard(t, c, askPlan).ID, true, "web")
	th, _ = c.thread(th.ID)
	a := c.conf().Assistants[th.Lead]
	src := threadSource{Name: "prs", Tool: "github/list", Items: &sourceItems{Path: "items", ID: "id"}}
	c.putThreadSource(th.ID, src, byBrain)
	list := make([]any, 250)
	for i := range list {
		list[i] = map[string]any{"id": strconv.Itoa(i)}
	}
	read := func() {
		c.keepSourceReads(th.ID, map[string]threadSource{"prs": src}, map[string]sourceRead{"prs": {value: map[string]any{"items": list}}}, time.Now())
	}
	read()
	if rows := itemRows(t, c, th.ID); len(rows) != threadItemsMax {
		t.Fatalf("%d rows shown", len(rows))
	}

	lead := connectServer(t, c.assistantServer(a, nil))
	c.updateAssistant(a.Name, func(st *AssistantStatus) { st.Tainted = []string{"mail:7"} })
	if out, msg := callOn(t, lead, "note_item", map[string]any{"source": "prs", "item": "230", "note": "À relire."}); msg != "" {
		t.Fatalf("note_item: %v %s", out, msg)
	}
	notes := c.itemNotes(th.ID)
	if len(notes) != 1 || notes[0].Author != "lead:"+strconv.FormatInt(th.ID, 10) || notes[0].Tainted != "mail:7" || notes[0].Gone != 0 {
		t.Fatalf("notes %+v", notes)
	}
	read()
	if notes = c.itemNotes(th.ID); notes[0].Gone != 0 {
		t.Errorf("an item listed past those shown is gone: %+v", notes)
	}

	c.updateAssistant(a.Name, func(st *AssistantStatus) { st.Tainted = nil })
	if out, msg := callOn(t, lead, "thread", map[string]any{}); msg != "" || out["notes"] == nil {
		t.Fatalf("thread: %v %s", out, msg)
	}
	if st := c.assistantStatus(a.Name); !slices.Contains(st.Tainted, "note:prs/230") {
		t.Errorf("reading a tainted note left its lead clean: %v", st.Tainted)
	}

	// Noted again once its item left: the clock goes on.
	c.store.db.Exec(`UPDATE thread_items SET gone = ? WHERE thread = ?`, time.Now().Add(-6*24*time.Hour).Unix(), th.ID)
	before := c.itemNotes(th.ID)[0].Gone
	list = list[:10]
	read()
	if _, err := c.noteItem(th.ID, noteItemIn{Source: "prs", Item: "230", Note: "Toujours à relire."}, byBrain, nil); err != nil {
		t.Fatal(err)
	}
	if after := c.itemNotes(th.ID)[0].Gone; after != before {
		t.Errorf("noted again, gone %d → %d", before, after)
	}
}
