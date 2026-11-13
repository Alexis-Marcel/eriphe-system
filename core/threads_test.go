package main

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The work sessions' tables as an eriphe-core before #465 made them.
const oldSessionsSchema = `CREATE TABLE IF NOT EXISTS sessions (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	title     TEXT NOT NULL,
	goal      TEXT NOT NULL,
	done_when TEXT NOT NULL DEFAULT '',
	state     TEXT NOT NULL,
	held      TEXT NOT NULL DEFAULT '',
	budget    INTEGER NOT NULL DEFAULT 0,
	amends    INTEGER NOT NULL DEFAULT 0,
	summary   TEXT NOT NULL DEFAULT '',
	tainted   TEXT NOT NULL DEFAULT '',
	lead      TEXT NOT NULL DEFAULT '',
	told      TEXT NOT NULL DEFAULT '',
	spent     INTEGER NOT NULL DEFAULT 0,
	since     INTEGER NOT NULL DEFAULT 0,
	created   INTEGER NOT NULL,
	started   INTEGER NOT NULL DEFAULT 0,
	ended     INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS session_steps (
	session  INTEGER NOT NULL,
	key      TEXT NOT NULL,
	n        INTEGER NOT NULL,
	title    TEXT NOT NULL,
	kind     TEXT NOT NULL,
	after    TEXT NOT NULL DEFAULT '',
	spec     TEXT NOT NULL DEFAULT '{}',
	status   TEXT NOT NULL,
	planned  INTEGER NOT NULL DEFAULT 0,
	task     TEXT NOT NULL DEFAULT '',
	tries    INTEGER NOT NULL DEFAULT 0,
	failed   INTEGER NOT NULL DEFAULT 0,
	evidence TEXT NOT NULL DEFAULT '',
	output   TEXT NOT NULL DEFAULT '',
	note     TEXT NOT NULL DEFAULT '',
	started  INTEGER NOT NULL DEFAULT 0,
	ended    INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (session, key)
);
CREATE INDEX IF NOT EXISTS session_steps_task ON session_steps (task);
CREATE TABLE IF NOT EXISTS session_asks (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	session INTEGER NOT NULL,
	kind    TEXT NOT NULL,
	step    TEXT NOT NULL DEFAULT '',
	payload TEXT NOT NULL DEFAULT '{}',
	why     TEXT NOT NULL DEFAULT '',
	state   TEXT NOT NULL DEFAULT 'pending',
	created INTEGER NOT NULL,
	decided INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS session_log (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	session INTEGER NOT NULL,
	at      INTEGER NOT NULL,
	who     TEXT NOT NULL,
	text    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS session_log_session ON session_log (session, id);
CREATE TABLE IF NOT EXISTS session_inbox (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	session   INTEGER NOT NULL,
	text      TEXT NOT NULL,
	taint     TEXT NOT NULL DEFAULT '',
	delivered INTEGER NOT NULL DEFAULT 0
);`

// A database with work sessions opens as threads, twice as once: every
// session a led thread, its phase kept and its state from it, its steps,
// asks, log and inbox under the new names, and the refs to it rewritten:
// cards, the attention queue, the journal, surfaces, kv, skill runs and a
// running lead's trigger.
func TestSessionsMigrateToThreads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "core.db")
	s, err := openStore(path) // today's other tables
	if err != nil {
		t.Fatal(err)
	}
	s.db.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`DROP TABLE threads`, `DROP TABLE thread_steps`, `DROP TABLE thread_asks`, `DROP TABLE thread_log`, `DROP TABLE thread_inbox`,
		oldSessionsSchema,
		`ALTER TABLE skill_runs RENAME COLUMN thread TO session`,
		`INSERT INTO sessions (title, goal, done_when, state, budget, lead, created, started) VALUES ('Brand name', 'Find a name.', 'Sam picked one', 'running', 60, 'lead-1', 1000, 1000)`,
		`INSERT INTO sessions (title, goal, state, created) VALUES ('Old', 'Something.', 'proposed', 1000)`,
		`INSERT INTO sessions (title, goal, state, created, ended) VALUES ('Paused', 'x', 'paused', 1000, 0)`,
		`INSERT INTO sessions (title, goal, state, created, ended) VALUES ('Done', 'x', 'done', 1000, 2000)`,
		`INSERT INTO sessions (title, goal, state, created, ended) VALUES ('Stopped', 'x', 'stopped', 1000, 2000)`,
		`INSERT INTO sessions (title, goal, state, created, ended) VALUES ('Declined', 'x', 'declined', 1000, 2000)`,
		`INSERT INTO session_steps (session, key, n, title, kind, status, task) VALUES (1, 'names', 1, 'Find names', 'task', 'running', 't-1')`,
		`INSERT INTO session_asks (session, kind, created) VALUES (2, 'plan', 1000)`,
		`INSERT INTO session_log (session, at, who, text) VALUES (1, 1000, 'engine', 'Session ouverte.')`,
		`INSERT INTO session_inbox (session, text) VALUES (1, 'Step done.')`,
		`INSERT INTO convo_cards (ref, kind, created) VALUES ('session:1', 'session', 1000)`,
		`INSERT INTO attention (ref, weight, source, time) VALUES ('session:2', 4, 'session:2', 1000)`,
		`INSERT INTO event_journal (kind, key, source, at) VALUES ('session.ended', 'session:4', 'session', 2000)`,
		`INSERT INTO surfaces (id, title, author, components, lifetime, created, updated) VALUES ('brand', 'Brand', 'lead:1', '[{"id":"p","component":"Proposal","ref":"session:1"}]', '{"while":"session:1"}', 1000, 1000)`,
		`INSERT INTO kv (key, value) VALUES ('last_ref', 'session:1')`,
		`INSERT INTO skill_runs (skill, sha, state, created, session) VALUES ('prod', 'abc', 'done', 1000, 1)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	// A running lead, as putAssistant keeps it.
	lead, _ := json.Marshal(Assistant{Role: assistantRoleLead, DisplayName: "Chef de session 1", Triggers: []string{"session:1"}})
	if _, err := db.Exec(`INSERT INTO catalog (kind, name, spec, updated) VALUES (?, 'lead-1', ?, 1000)`, kindAssistant, string(lead)); err != nil {
		t.Fatal(err)
	}
	db.Close()

	for range 2 {
		s, err := openStore(path)
		if err != nil {
			t.Fatal(err)
		}
		c := &core{store: s}
		ss, err := c.threads(false, 10)
		if err != nil || len(ss) != 6 {
			t.Fatalf("threads %+v %v", ss, err)
		}
		want := map[string][2]string{"Brand name": {"open", "running"}, "Old": {"waiting", "proposed"}, "Paused": {"waiting", "paused"},
			"Done": {"done", "done"}, "Stopped": {"dropped", "stopped"}, "Declined": {"dropped", "declined"}}
		for _, th := range ss {
			if w := want[th.Title]; th.Mode != modeLed || th.State != w[0] || th.Phase != w[1] {
				t.Errorf("%s: mode %s, state %s, phase %s; want led, %s, %s", th.Title, th.Mode, th.State, th.Phase, w[0], w[1])
			}
		}
		if th, _ := c.thread(1); th.Lead != "lead-1" || th.DoneWhen != "Sam picked one" || th.Budget != 60 || !th.active() {
			t.Errorf("thread 1 %+v", th)
		}
		if st, ok := c.stepOfTask("t-1"); !ok || st.Thread != 1 || st.Key != "names" {
			t.Errorf("step %+v %v", st, ok)
		}
		if a, err := c.threadAsk(1); err != nil || a.Thread != 2 || a.Kind != askPlan {
			t.Errorf("ask %+v %v", a, err)
		}
		if l := c.threadLog(1, 5); len(l) != 1 {
			t.Errorf("log %+v", l)
		}
		one := func(q string, want string) {
			t.Helper()
			var got string
			if err := s.db.QueryRow(q).Scan(&got); err != nil || got != want {
				t.Errorf("%s: %q %v, want %q", q, got, err, want)
			}
		}
		one(`SELECT text FROM thread_inbox WHERE thread = 1`, "Step done.")
		one(`SELECT kind || ' ' || ref FROM convo_cards`, "thread thread:1")
		one(`SELECT ref || ' ' || source FROM attention`, "thread:2 thread:2")
		one(`SELECT kind || ' ' || key || ' ' || source FROM event_journal`, "thread.ended thread:4 thread")
		one(`SELECT lifetime FROM surfaces WHERE id = 'brand'`, `{"while":"thread:1"}`)
		one(`SELECT components FROM surfaces WHERE id = 'brand'`, `[{"id":"p","component":"Proposal","ref":"thread:1"}]`)
		one(`SELECT value FROM kv WHERE key = 'last_ref'`, "thread:1")
		one(`SELECT CAST(thread AS TEXT) FROM skill_runs`, "1")
		var spec string
		s.db.QueryRow(`SELECT spec FROM catalog WHERE kind = ? AND name = 'lead-1'`, kindAssistant).Scan(&spec)
		var a Assistant
		if err := json.Unmarshal([]byte(spec), &a); err != nil || leadThread(a) != 1 || a.DisplayName != "Chef du thread 1" {
			t.Errorf("the lead %+v %v", a, err)
		}
		one(`SELECT COUNT(*) FROM sqlite_master WHERE name LIKE 'session%'`, "0")
		one(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name IN ('thread_steps_task', 'thread_log_thread', 'threads_subject')`, "3")
		// New threads follow the old ones.
		th, err := c.openThread(threadIn{Title: "Next", Goal: "More."}, nil, byBrain)
		if err != nil || th.ID != 7 {
			t.Fatalf("a new thread %+v %v", th, err)
		}
		s.db.Exec(`DELETE FROM threads WHERE id = 7`)
		s.db.Exec(`DELETE FROM sqlite_sequence WHERE name = 'threads'`)
		s.db.Exec(`INSERT INTO sqlite_sequence (name, seq) VALUES ('threads', 6)`)
		s.db.Close()
	}
}

// A light thread opens at once: no card, no budget, no lead; it is held
// by the brain. It takes no plan.
func TestLightThreadOpensAtOnce(t *testing.T) {
	c := startCore(t)
	out, isErr := callBrainTool(t, c, "open_thread", map[string]any{
		"title": "PR alerting", "goal": "The alerting PR is merged and deployed.", "subject": "pr:example/infra#12"})
	if isErr || strings.Contains(out["note"].(string), "Only proposed") {
		t.Fatalf("open_thread: %v", out)
	}
	ss, _ := c.threads(true, 10)
	if len(ss) != 1 || ss[0].Mode != modeLight || ss[0].State != threadOpen || ss[0].Phase != "" || ss[0].Budget != 0 || ss[0].Subject != "pr:example/infra#12" {
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
		"bad pr":    {Title: "A", Goal: "B", Subject: "pr:example"},
		"a record":  {Title: "A", Goal: "B", Subject: "task:t-1"},
		"led empty": {Title: "A", Goal: "B", Mode: modeLed},
	} {
		if _, err := c.openThread(in, nil, byBrain); err == nil {
			t.Errorf("%s: opened", name)
		}
	}
}

// find_thread finds a thread by its subject, any case, or by words; with
// neither, the active ones; the ended ones too when asked by subject.
func TestFindThread(t *testing.T) {
	c := startCore(t)
	open := func(title, goal, subject string) Thread {
		t.Helper()
		th, err := c.openThread(threadIn{Title: title, Goal: goal, Subject: subject}, nil, byBrain)
		if err != nil {
			t.Fatal(err)
		}
		return th
	}
	pr := open("PR alerting", "Merged and deployed.", "pr:example/infra#12")
	open("Wardrobe", "Find a wardrobe for the bedroom.", "project:bedroom")
	done := open("Dentist", "An appointment is booked.", "event:42")
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
	if got := find(map[string]any{"subject": "PR:example/infra#12"}); len(got) != 1 || got[0] != pr.Title {
		t.Errorf("by subject: %v", got)
	}
	if got := find(map[string]any{"text": "wardrobe bedroom"}); len(got) != 1 || got[0] != "Wardrobe" {
		t.Errorf("by words: %v", got)
	}
	if got := find(map[string]any{"subject": "event:42"}); len(got) != 1 || got[0] != "Dentist" {
		t.Errorf("an ended one by subject: %v", got)
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
	th, err := c.openThread(threadIn{Title: "PR alerting", Goal: "Merged and deployed.", Subject: "pr:example/infra#12"}, nil, byBrain)
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
	if th.State != threadOpen || !th.Ended.IsZero() || th.Subject != "pr:example/infra#12" {
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
