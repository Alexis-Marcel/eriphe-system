package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// reportFixture fills a store with the brain's turns and worker tasks
// around a window of 7 days before now.
func reportFixture(t *testing.T, s *Store, now time.Time) {
	t.Helper()
	since := now.Add(-7 * 24 * time.Hour)
	task := func(id, kind, prompt string, created time.Time) {
		if _, err := s.db.Exec(`INSERT INTO tasks (id, device, runtime, dir, prompt, state, kind, created, updated)
			VALUES (?, 'd', 'claude-code', '/w', ?, 'done', ?, ?, ?)`, id, prompt, kind, created.Unix(), created.Unix()); err != nil {
			t.Fatal(err)
		}
	}
	seqs := map[string]int{}
	event := func(id, typ string, at time.Time, data map[string]any) {
		seqs[id]++
		raw, _ := json.Marshal(data)
		if _, err := s.db.Exec(`INSERT INTO events (task_id, seq, time, type, data) VALUES (?, ?, ?, ?, ?)`,
			id, seqs[id], at.Unix(), typ, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	tool := func(id, name string, at time.Time, input map[string]any) {
		event(id, "tool_use", at, map[string]any{"name": name, "input": input})
	}
	end := func(id string, at time.Time, ms int64, usage map[string]any) {
		d := map[string]any{"subtype": "success"}
		if ms > 0 {
			d["duration_ms"] = ms
		}
		if usage != nil {
			d["usage"] = usage
		}
		event(id, "turn_end", at, d)
	}
	day := 24 * time.Hour

	task("b-1", "brain", "", now.Add(-30*day))
	// Before the window: not counted.
	tool("b-1", "mcp__eriphe-core__node_write", since.Add(-day), nil)
	end("b-1", since.Add(-day), 99000, map[string]any{"output_tokens": 9000})
	// A turn that began before the window and ended in it: counted whole.
	tool("b-1", "mcp__eriphe-core__node_write", since.Add(-10*time.Second), nil)
	end("b-1", since.Add(10*time.Second), 40000, map[string]any{"output_tokens": 4000, "context_tokens": 50000})
	// A launch: its prompt counted in characters, not bytes.
	tool("b-1", "mcp__eriphe-core__run_task", now.Add(-5*day), map[string]any{"prompt": strings.Repeat("é", 1200)})
	tool("b-1", "Bash", now.Add(-5*day), nil)
	end("b-1", now.Add(-5*day), 60000, map[string]any{"output_tokens": 3000, "input_tokens": 100, "cache_read_tokens": 900})
	end("b-1", now.Add(-4*day), 5000, map[string]any{"output_tokens": 100, "context_tokens": 20000})
	end("b-1", now.Add(-3*day), 10000, map[string]any{"output_tokens": 200, "context_tokens": 30000})
	// A failed turn: counted, not measured.
	end("b-1", now.Add(-3*day), 0, nil)
	// A turn still going: not counted.
	tool("b-1", "mcp__eriphe-core__node_write", now.Add(-time.Minute), nil)

	task("b-2", "brain", "", now.Add(-2*day))
	end("b-2", now.Add(-day), 20000, map[string]any{"output_tokens": 500, "context_tokens": 10000})
	end("b-2", now.Add(-day), 30000, map[string]any{"output_tokens": 50, "context_tokens": 5000}) // 30 s: not over

	// Not the brain's.
	task("m-1", "memory", "", now.Add(-day))
	end("m-1", now.Add(-day), 500000, map[string]any{"output_tokens": 1})
	task("a-1", "home", "", now.Add(-day))
	tool("a-1", "mcp__eriphe-core__run_task", now.Add(-day), map[string]any{"ask": "x"})
	end("a-1", now.Add(-day), 500000, map[string]any{"output_tokens": 1})

	// Worker tasks: a skill's run, another, and one before the window.
	task("w-1", "worker", "abc", now.Add(-2*day))
	task("w-2", "worker", "a long prompt", now.Add(-2*day))
	tool("w-2", "mcp__eriphe-core__node_read", now.Add(-2*day), nil)
	tool("w-2", "mcp__eriphe-core__node_find", now.Add(-2*day), nil)
	// Its own node named, and another: only the other is by others.
	if _, err := s.db.Exec(`INSERT INTO task_nodes (task, node) VALUES ('w-2', 'n-own')`); err != nil {
		t.Fatal(err)
	}
	tool("w-2", "mcp__eriphe-nodes__node_read", now.Add(-2*day), map[string]any{"id": "n-own"})
	tool("w-2", "mcp__eriphe-nodes__node_read", now.Add(-2*day), map[string]any{"id": "n-parent"})
	tool("w-2", "mcp__eriphe-core__node_write", now.Add(-2*day), nil)
	tool("w-2", "Read", now.Add(-2*day), nil)
	task("w-3", "worker", "old", now.Add(-10*day))
	tool("w-3", "mcp__eriphe-core__node_read", now.Add(-10*day), nil)
	for _, id := range []string{"w-1", "w-3"} {
		if _, err := s.db.Exec(`INSERT INTO skill_runs (skill, sha, task, state, created) VALUES ('tri', 'x', ?, 'done', ?)`,
			id, now.Unix()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTurnReport(t *testing.T) {
	tw := newTestWeb(t)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	reportFixture(t, tw.c.store, now)
	rep, err := tw.c.store.turnReport(now.Add(-7*24*time.Hour), 7)
	if err != nil {
		t.Fatal(err)
	}
	b := rep.Brain
	if b.Turns != 7 {
		t.Errorf("turns: %d, want 7", b.Turns)
	}
	// Nearest rank over 5000, 10000, 20000, 30000, 40000, 60000.
	if want := (spread{N: 6, Median: 20000, P75: 40000, P90: 60000, Max: 60000}); b.DurationMS != want {
		t.Errorf("duration: %+v, want %+v", b.DurationMS, want)
	}
	if b.SlowTurns != 2 || b.SlowShare != 0.606 { // 100 s of 165 s
		t.Errorf("slow: %d turns, share %v", b.SlowTurns, b.SlowShare)
	}
	if b.OutputTokens.N != 6 || b.OutputTokens.Median != 200 || b.OutputTokens.Max != 4000 {
		t.Errorf("output tokens: %+v", b.OutputTokens)
	}
	if b.ContextTokens.N != 6 || b.ContextTokens.Median != 10000 || b.ContextTokens.Max != 50000 {
		t.Errorf("context tokens: %+v", b.ContextTokens)
	}
	if len(b.Tools) != 3 || b.Tools["node_write"] != 1 || b.Tools["run_task"] != 1 || b.Tools["Bash"] != 1 {
		t.Errorf("tools: %v", b.Tools)
	}
	l := rep.Launches
	if l.Turns != 1 || l.RunTaskCalls != 1 || l.DurationMS.Median != 60000 || l.OutputTokens.Median != 3000 {
		t.Errorf("launches: %+v", l)
	}
	if l.BrainPromptChars.Median != 1200 || l.SkillRuns != 1 || l.SkillPromptChars.Median != 3 || l.OtherTasks != 1 {
		t.Errorf("launches: %+v", l)
	}
	r := rep.Reads
	if r.Tasks != 2 || r.TasksReading != 1 || r.Calls.Median != 0 || r.Calls.Max != 4 ||
		r.ByOthers != (spread{N: 2, Median: 0, P75: 1, P90: 1, Max: 1}) {
		t.Errorf("reads: %+v", r)
	}
	if len(rep.TurnList) != 7 {
		t.Fatalf("turn list: %d turns", len(rep.TurnList))
	}
	first, last := rep.TurnList[0], rep.TurnList[6]
	if *first.DurationMS != 60000 || first.Launches != 1 || first.Tools["Bash"] != 1 || *first.ContextTokens != 1000 {
		t.Errorf("longest turn: %+v", first)
	}
	if last.DurationMS != nil || last.OutputTokens != nil || len(last.Tools) != 0 {
		t.Errorf("unmeasured turn last: %+v", last)
	}
}

func TestTurnReportEmpty(t *testing.T) {
	tw := newTestWeb(t)
	rep, err := tw.c.store.turnReport(time.Now().Add(-7*24*time.Hour), 7)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Brain.Turns != 0 || rep.Brain.SlowShare != 0 || rep.Brain.DurationMS != (spread{}) || rep.TurnList == nil {
		t.Errorf("empty report: %+v", rep)
	}
}

func TestTurnReportRoute(t *testing.T) {
	tw := newTestWeb(t)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	tw.w.now = func() time.Time { return now }
	reportFixture(t, tw.c.store, now)
	tw.setup()

	// Without the owner's session: refused.
	req, _ := http.NewRequest("GET", tw.srv.URL+"/api/report/turns", nil)
	resp, err := tw.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("without a session: %d", resp.StatusCode)
	}

	code, body, _ := tw.do("GET", "/api/report/turns", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("report: %d %s", code, body)
	}
	var rep turnReport
	if err := json.Unmarshal([]byte(body), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Days != 7 || rep.Brain.Turns != 7 || rep.Launches.RunTaskCalls != 1 {
		t.Errorf("report: %+v", rep)
	}
	if !strings.Contains(body, `"by_others":{"n":2,`) {
		t.Errorf("by_others: %s", body)
	}

	// One day: only b-2's two turns.
	if code, body, _ = tw.do("GET", "/api/report/turns?days=1", nil, nil); code != http.StatusOK {
		t.Fatalf("one day: %d", code)
	}
	json.Unmarshal([]byte(body), &rep)
	if rep.Days != 1 || rep.Brain.Turns != 2 {
		t.Errorf("one day: %d days, %d turns", rep.Days, rep.Brain.Turns)
	}
	for _, q := range []string{"0", "91", "x"} {
		if code, _, _ := tw.do("GET", "/api/report/turns?days="+q, nil, nil); code != http.StatusBadRequest {
			t.Errorf("days=%s: %d", q, code)
		}
	}
}

// The delegated work: the tasks the brain's run_task started in the
// window, how many ended (a first turn_end, else a stop, failure or loss),
// and the time from the user's message their turn answered to that end.
func TestTurnReportDelegated(t *testing.T) {
	tw := newTestWeb(t)
	s := tw.c.store
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	msg := func(seq int, at time.Time) {
		exec(`INSERT INTO messages (seq, time, role, text, delivered) VALUES (?, ?, ?, 'x', 1)`, seq, at.Unix(), roleUser)
	}
	launched := func(id string, created time.Time, msg int) {
		exec(`INSERT INTO tasks (id, device, runtime, dir, prompt, state, kind, created, updated)
			VALUES (?, 'd', 'claude-code', '/w', 'x', 'idle', 'worker', ?, ?)`, id, created.Unix(), now.Unix())
		if msg >= 0 {
			exec(`INSERT INTO task_launches (task, msg) VALUES (?, ?)`, id, msg)
		}
	}
	turnEnd := func(id string, seq int, at time.Time) {
		exec(`INSERT INTO events (task_id, seq, time, type, data) VALUES (?, ?, ?, 'turn_end', '{}')`, id, seq, at.Unix())
	}
	logged := func(id, state string, at time.Time) {
		exec(`INSERT INTO task_log (task_id, at, state) VALUES (?, ?, ?)`, id, at.Unix(), state)
	}
	// Ended by its first turn: 60 s after the message.
	t1 := now.Add(-2 * day)
	msg(1, t1.Add(-10*time.Second))
	launched("d-1", t1, 1)
	turnEnd("d-1", 1, t1.Add(50*time.Second))
	turnEnd("d-1", 2, t1.Add(time.Hour))
	// Stopped before a turn ended: 120 s.
	t2 := now.Add(-day)
	msg(2, t2)
	launched("d-2", t2, 2)
	logged("d-2", stateRunning, t2.Add(5*time.Second))
	logged("d-2", stateStopped, t2.Add(120*time.Second))
	// Launched in a turn of events: counted, not timed.
	launched("d-3", t2, 0)
	turnEnd("d-3", 1, t2.Add(time.Minute))
	// Still at work: counted, not ended.
	launched("d-4", t2, 2)
	// Before the window, and a task the brain's run_task did not start.
	launched("d-5", now.Add(-10*day), 2)
	turnEnd("d-5", 1, now.Add(-9*day))
	launched("w-1", t2, -1)
	turnEnd("w-1", 1, t2.Add(time.Second))
	// Stopped before a device took it: no task_log, its row says when.
	launched("d-6", t2, 2)
	exec(`UPDATE tasks SET state = ? WHERE id = 'd-6'`, statePending)
	if err := tw.c.stop(context.Background(), "d-6"); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE tasks SET updated = ? WHERE id = 'd-6'`, t2.Add(90*time.Second).Unix())

	rep, err := s.turnReport(now.Add(-7*day), 7)
	if err != nil {
		t.Fatal(err)
	}
	d := rep.Delegated
	if d.Tasks != 5 || d.Ended != 4 || d.FromMessageMS != (spread{N: 3, Median: 90000, P75: 120000, P90: 120000, Max: 120000}) {
		t.Errorf("delegated: %+v", d)
	}
}

// run_task records the message of the user's its turn answers; a turn of
// events only records none.
func TestRunTaskRecordsLaunch(t *testing.T) {
	c := nodeCore(t)
	seq := say(t, c.core, roleUser, "Cherche un hôtel", true)
	launch := func() int {
		t.Helper()
		out, err := c.brainRunTask(context.Background(), launchRequest{Title: "Recherche", Ask: "Cherche."})
		if err != nil {
			t.Fatal(err)
		}
		var msg int
		if err := c.store.db.QueryRow(`SELECT msg FROM task_launches WHERE task = ?`, out["task"]).Scan(&msg); err != nil {
			t.Fatal(err)
		}
		return msg
	}
	c.turn.start(seq, seq, 0)
	if msg := launch(); msg != seq {
		t.Errorf("a user's turn: msg %d, want %d", msg, seq)
	}
	c.turn.goOn(0, 0, 0, nil)
	if msg := launch(); msg != 0 {
		t.Errorf("a turn of events: msg %d", msg)
	}
}
