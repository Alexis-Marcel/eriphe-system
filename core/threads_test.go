package main

import (
	"strings"
	"testing"
	"time"
)

// A thread opens at once, with no card; it is held by the brain. It
// takes no plan: work cut in steps runs in a node (node_steps.go).
func TestLightThreadOpensAtOnce(t *testing.T) {
	c := startCore(t)
	out, isErr := callBrainTool(t, c, "open_thread", map[string]any{
		"title": "PR alerting", "goal": "The alerting PR is merged and deployed.", "alias": "pr:example/infra#12"})
	if isErr || strings.Contains(out["note"].(string), "Only proposed") {
		t.Fatalf("open_thread: %v", out)
	}
	ss, _ := c.threads(true, 10)
	if len(ss) != 1 || ss[0].State != threadOpen || ss[0].Alias != "pr:example/infra#12" {
		t.Fatalf("threads %+v", ss)
	}
	if xs, _ := c.cardsWhere(`state = ?`, cardPending); len(xs) != 0 {
		t.Fatalf("a thread asked a card: %+v", xs)
	}
	c.threadsTick(time.Now())
	if th, _ := c.thread(ss[0].ID); th.State != threadOpen {
		t.Errorf("after a tick: %+v", th)
	}
	if out, isErr := callBrainTool(t, c, "open_thread", map[string]any{"title": "A", "goal": "B", "alias": "topic:a",
		"steps": []any{map[string]any{"key": "a", "title": "A", "prompt": "x"}}}); !isErr {
		t.Errorf("a thread opened with steps: %v", out)
	}
	for name, in := range map[string]threadIn{
		"no goal":  {Title: "A"},
		"bad pr":   {Title: "A", Goal: "B", Alias: "pr:example"},
		"a record": {Title: "A", Goal: "B", Alias: "task:t-1"},
	} {
		if _, err := c.openThread(in, nil, byBrain); err == nil {
			t.Errorf("%s: opened", name)
		}
	}
}

// find_thread finds a thread by its alias, any case, or by words; with
// neither, the active ones; the ended ones too when asked by alias.
func TestFindThread(t *testing.T) {
	c := startCore(t)
	open := func(title, goal, subject string) Thread {
		t.Helper()
		th, err := c.openThread(threadIn{Title: title, Goal: goal, Alias: subject}, nil, byBrain)
		if err != nil {
			t.Fatal(err)
		}
		return th
	}
	pr := open("PR alerting", "Merged and deployed.", "pr:example/infra#12")
	open("Wardrobe", "Find a wardrobe for the bedroom.", "project:bedroom")
	done := open("Dentist", "An appointment is booked.", "project:dentist")
	if err := c.closeLight(done.ID, "Booked for Tuesday.", "brain"); err != nil {
		t.Fatal(err)
	}
	find := func(args map[string]any) []string {
		t.Helper()
		out, isErr := callBrainTool(t, c, "find_thread", args)
		if isErr {
			t.Fatalf("find_thread %v: %v", args, out)
		}
		var titles []string
		list, _ := out["threads"].([]any)
		for _, x := range list {
			titles = append(titles, x.(map[string]any)["title"].(string))
		}
		return titles
	}
	if got := find(map[string]any{"alias": "PR:example/infra#12"}); len(got) != 1 || got[0] != pr.Title {
		t.Errorf("by alias: %v", got)
	}
	if got := find(map[string]any{"text": "wardrobe bedroom"}); len(got) != 1 || got[0] != "Wardrobe" {
		t.Errorf("by words: %v", got)
	}
	if got := find(map[string]any{"alias": "projet:Dentist"}); len(got) != 1 || got[0] != "Dentist" {
		t.Errorf("an ended one by alias: %v", got)
	}
	if got := find(map[string]any{"text": "100%"}); len(got) != 0 {
		t.Errorf("a wildcard matched: %v", got)
	}
	if got := find(map[string]any{}); len(got) != 2 {
		t.Errorf("active: %v", got)
	}
	if got := find(map[string]any{"all": true}); len(got) != 3 {
		t.Errorf("all: %v", got)
	}
}

// A thread closes only on its goal, with how; it reopens when its subject
// comes back, light, its history kept; stopped, it is dropped. A led
// thread that ended reopens light: its lead went with its end.
func TestCloseAndReopenThread(t *testing.T) {
	c := startCore(t)
	th, err := c.openThread(threadIn{Title: "PR alerting", Goal: "Merged and deployed.", Alias: "pr:example/infra#12"}, nil, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	if out, isErr := callBrainTool(t, c, "close_thread", map[string]any{"thread_id": th.ID, "summary": " "}); !isErr {
		t.Fatalf("closed without how: %v", out)
	}
	if out, isErr := callBrainTool(t, c, "reopen_thread", map[string]any{"thread_id": th.ID, "why": "again"}); !isErr {
		t.Fatalf("an open thread reopened: %v", out)
	}
	if out, isErr := callBrainTool(t, c, "close_thread", map[string]any{"thread_id": th.ID, "summary": "Merged and deployed on Monday."}); isErr {
		t.Fatalf("close_thread: %v", out)
	}
	th, _ = c.thread(th.ID)
	if th.State != threadDone || th.Summary != "Merged and deployed on Monday." || th.Ended.IsZero() || th.active() {
		t.Fatalf("closed %+v", th)
	}
	var ended int
	c.store.db.QueryRow(`SELECT COUNT(*) FROM event_journal WHERE kind = ? AND key = ?`, evThreadEnded, "thread:1").Scan(&ended)
	if ended != 1 {
		t.Errorf("thread.ended told %d times", ended)
	}
	out, isErr := callBrainTool(t, c, "reopen_thread", map[string]any{"thread_id": th.ID, "why": "The deploy was rolled back."})
	if isErr {
		t.Fatalf("reopen_thread: %v", out)
	}
	th, _ = c.thread(th.ID)
	if th.State != threadOpen || !th.Ended.IsZero() || th.Alias != "pr:example/infra#12" {
		t.Fatalf("reopened %+v", th)
	}
	if l := c.threadLog(th.ID, 10); len(l) != 3 || !strings.Contains(l[2].Text, "rolled back") {
		t.Errorf("log %+v", l)
	}
	if out, isErr := callBrainTool(t, c, "stop_thread", map[string]any{"thread_id": th.ID}); isErr {
		t.Fatalf("stop_thread: %v", out)
	}
	if th, _ = c.thread(th.ID); th.State != threadDropped {
		t.Fatalf("stopped %+v", th)
	}
}

// A fresh core.db has every column the one-shot fixes used to add, an
// about where records said subject, and one thread a key.
func TestFreshStoreSchema(t *testing.T) {
	c := startCore(t)
	db := c.store.db
	want := map[string][]string{
		"threads":       {"summarized", "seen", "visited"},
		"attention":     {"tainted", "delivered", "due"},
		"notifications": {"weight"},
		"skill_runs":    {"thread"},
	}
	for _, table := range aboutTables {
		want[table] = append(want[table], "about")
	}
	for table, cols := range want {
		for _, col := range cols {
			var n int
			db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, col).Scan(&n)
			if n != 1 {
				t.Errorf("%s has no %s", table, col)
			}
		}
	}
	for _, table := range append([]string{"threads"}, aboutTables...) {
		var n int
		db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = 'subject'`, table).Scan(&n)
		if n != 0 {
			t.Errorf("%s still says subject", table)
		}
	}
	if _, err := db.Exec(`INSERT INTO names (name, target, seen) VALUES ('project:site', 'thread:1', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO names (name, target, seen) VALUES ('project:site', 'thread:2', 1)`); err == nil {
		t.Error("two threads have one key")
	}
	if _, err := db.Exec(`INSERT INTO names (name, target, seen) VALUES ('le site', 'thread:2', 1)`); err != nil {
		t.Errorf("a name two threads go by: %v", err)
	}
}
