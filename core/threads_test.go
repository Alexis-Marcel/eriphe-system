package main

import (
	"strings"
	"testing"
	"time"
)

// A light thread opens at once: no card, no budget, no lead; it is held
// by the brain. It takes no plan.
func TestLightThreadOpensAtOnce(t *testing.T) {
	c := startCore(t)
	out, isErr := callBrainTool(t, c, "open_thread", map[string]any{
		"title": "PR alerting", "goal": "The alerting PR is merged and deployed.", "alias": "pr:example/infra#12"})
	if isErr || strings.Contains(out["note"].(string), "Only proposed") {
		t.Fatalf("open_thread: %v", out)
	}
	ss, _ := c.threads(true, 10)
	if len(ss) != 1 || ss[0].Mode != modeLight || ss[0].State != threadOpen || ss[0].Phase != "" || ss[0].Budget != 0 || ss[0].Alias != "pr:example/infra#12" {
		t.Fatalf("threads %+v", ss)
	}
	if xs, _ := c.cardsWhere(`kind = ?`, cardThread); len(xs) != 0 {
		t.Fatalf("a light thread asked a card: %+v", xs)
	}
	var asks int
	c.store.db.QueryRow(`SELECT COUNT(*) FROM thread_asks`).Scan(&asks)
	if asks != 0 || c.hasLead(ss[0]) {
		t.Fatalf("asks %d, lead %q", asks, ss[0].Lead)
	}
	// The engine's tick leaves it alone; a plan is a led thread's.
	c.threadsTick(time.Now())
	if th, _ := c.thread(ss[0].ID); th.State != threadOpen {
		t.Errorf("after a tick: %+v", th)
	}
	if out, isErr := callBrainTool(t, c, "settle_step", map[string]any{"thread_id": ss[0].ID, "step": "x", "verdict": "accept"}); !isErr {
		t.Errorf("a light thread's step was judged: %v", out)
	}
	for name, in := range map[string]threadIn{
		"steps":     {Title: "A", Goal: "B", Mode: modeLight, Steps: []stepSpec{{Key: "a", Title: "A", Prompt: "x"}}},
		"budget":    {Title: "A", Goal: "B", Budget: 30},
		"no goal":   {Title: "A"},
		"bad mode":  {Title: "A", Goal: "B", Mode: "heavy"},
		"bad pr":    {Title: "A", Goal: "B", Alias: "pr:example"},
		"a record":  {Title: "A", Goal: "B", Alias: "task:t-1"},
		"led empty": {Title: "A", Goal: "B", Mode: modeLed},
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
	if th, _ = c.thread(th.ID); th.State != threadDropped || th.Phase != "" {
		t.Fatalf("stopped %+v", th)
	}

	// A led thread, stopped, reopens light.
	c2, led := threadCore(t)
	if err := c2.stopThread(t.Context(), led.ID, "brain"); err != nil {
		t.Fatal(err)
	}
	if led, _ = c2.thread(led.ID); led.State != threadDropped || led.Phase != phaseStopped {
		t.Fatalf("led stopped %+v", led)
	}
	if led, err = c2.reopenThread(led.ID, "Sam wants a name after all.", "brain"); err != nil || led.Mode != modeLight || led.State != threadOpen || led.Phase != "" {
		t.Fatalf("led reopened %+v %v", led, err)
	}
	if steps, _ := c2.steps(led.ID); len(steps) != 4 {
		t.Errorf("its steps were lost: %d", len(steps))
	}
}

// A fresh core.db has every column the one-shot fixes used to add, an
// about where records said subject, and one thread a key.
func TestFreshStoreSchema(t *testing.T) {
	c := startCore(t)
	db := c.store.db
	want := map[string][]string{
		"threads":       {"mode", "phase", "summarized", "seen", "visited"},
		"thread_steps":  {"card", "due", "tainted"},
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
