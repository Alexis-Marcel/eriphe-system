package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// « Je suis à Lyon jusqu'à dimanche » is a situation until Sunday 23:59:
// the brain's turn and a later task show it; Monday it is past.
func TestSituationNow(t *testing.T) {
	c, clock := testMemoryCore(t) // Monday 2026-10-26 14:00
	seq := ownerSays(c, "Je suis en vacances à Lyon jusqu'à dimanche")
	out, err := c.brainRemember(rememberIn{Text: "Sam est en vacances à Lyon", Kind: kindSituation, ValidTo: "2026-11-01", SourceRef: ref(seq)})
	if err != nil || out["status"] != memActive {
		t.Fatalf("%v %v", out, err)
	}
	x, _ := c.mem.memory(out["id"].(int64))
	if x.Subject != "owner" || x.ValidTo.Format("2006-01-02 15:04") != "2026-11-01 23:59" {
		t.Fatalf("%+v", x)
	}
	now := clock.now()
	if h := c.turnHead(now); !strings.Contains(h, "[en ce moment]\n- Sam est en vacances à Lyon (jusqu'au 2026-11-01 23:59)") {
		t.Fatalf("turn head %q", h)
	}
	task, err := c.startTask(context.Background(), "worker", runRequest{Prompt: "Trouve un restaurant pour ce soir"}, nodeTaskSpec{})
	if err != nil {
		t.Fatal(err)
	}
	var raw string
	c.store.db.QueryRow(`SELECT spec FROM tasks WHERE id = ?`, task.ID).Scan(&raw)
	var spec nodeTaskSpec
	json.Unmarshal([]byte(raw), &spec)
	if !strings.Contains(spec.AppendSystemPrompt, "vacances à Lyon") || !strings.Contains(spec.AppendSystemPrompt, "recall") {
		t.Fatalf("task instructions %q", spec.AppendSystemPrompt)
	}
	if b := c.nowBlock(time.Date(2026, 11, 2, 9, 0, 0, 0, time.Local)); b != "" {
		t.Fatalf("Monday: %q", b)
	}
	if _, err := c.brainRemember(rememberIn{Text: "Sam est à Lyon", Kind: kindSituation, SourceRef: ref(seq)}); err == nil {
		t.Fatal("a situation without its end")
	}
}

// One read from someone else's content shows only once approved.
func TestSituationExternal(t *testing.T) {
	c, clock := testMemoryCore(t)
	ownerSays(c, "Regarde mes mails")
	c.brainCalledCatalog("t-brain", "mail", "read_message")
	out, err := c.brainRemember(rememberIn{Text: "Sam est en déplacement à Nantes", Kind: kindSituation, ValidTo: "2026-10-30"})
	if err != nil || out["status"] != memProposed {
		t.Fatalf("%v %v", out, err)
	}
	if b := c.nowBlock(clock.now()); b != "" {
		t.Fatalf("shown before approval: %q", b)
	}
	if _, err := c.mem.approve(out["id"].(int64), "owner"); err != nil {
		t.Fatal(err)
	}
	if b := c.nowBlock(clock.now()); !strings.Contains(b, "Nantes") {
		t.Fatalf("approved: %q", b)
	}
}

func callMem(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("%s: %+v", name, res.Content[0])
	}
	out := map[string]any{}
	b, _ := json.Marshal(res.StructuredContent)
	json.Unmarshal(b, &out)
	return out
}

// A task recalls; a triggered skill's task also remembers, only proposed
// when it read someone else's content; the memory tools need no card.
func TestTaskMemory(t *testing.T) {
	c, _ := testMemoryCore(t)
	seq := ownerSays(c, "J'adore la cuisine lyonnaise")
	c.brainRemember(rememberIn{Text: "Sam adore la cuisine lyonnaise", Kind: kindPreference, SourceRef: ref(seq)})
	c.store.insertTask(&TaskRow{ID: "t-plain", Kind: "worker", Runtime: "claude-code", State: stateRunning, Prompt: "x"})
	c.store.insertTask(&TaskRow{ID: "t-run", Kind: "worker", Runtime: "claude-code", State: stateRunning, Prompt: "x"})
	c.store.db.Exec(`INSERT INTO skill_runs (skill, sha, key, events, task, state, created) VALUES ('cal', '', '', '[]', 't-run', ?, ?)`,
		skillRunOpen, time.Now().Unix())
	plain, _ := c.store.task("t-plain")
	cs := connectTo(t, c.taskMemoryServer(plain))
	tools, _ := cs.ListTools(context.Background(), nil)
	if len(tools.Tools) != 1 || tools.Tools[0].Name != "recall" {
		t.Fatalf("a plain task's tools %+v", tools.Tools)
	}
	if out := callMem(t, cs, "recall", map[string]any{"query": "cuisine"}); !strings.Contains(mustJSON(out), "lyonnaise") {
		t.Fatalf("recall %v", out)
	}
	run, _ := c.store.task("t-run")
	rs := connectTo(t, c.taskMemoryServer(run))
	out := callMem(t, rs, "remember", map[string]any{"text": "Sam est en déplacement à Lyon", "kind": "situation", "valid_to": "2026-10-28"})
	if out["status"] != memActive {
		t.Fatalf("a clean run's memory %v", out)
	}
	c.store.set(taskTaintKey("t-run"), "mail.read_message")
	out = callMem(t, rs, "remember", map[string]any{"text": "Sam dîne chez Paul jeudi", "kind": "episode"})
	if out["status"] != memProposed {
		t.Fatalf("a tainted run's memory %v", out)
	}
	if _, err := c.taskRemember("t-run", taskRememberIn{Text: "Sam préfère le thé", Kind: kindPreference}); err == nil {
		t.Fatal("a task wrote a preference")
	}
	ev := []EventRow{{Seq: 1, Time: time.Now(), Type: "permission", Data: map[string]any{"id": "p1", "tool_name": "mcp__eriphe-memory__recall", "input": map[string]any{}}}}
	c.store.addEvents("t-plain", ev)
	c.applyPolicy("t-plain", ev)
	if items, _ := c.store.inbox("t-plain"); len(items) != 1 || items[0].Data["allow"] != true {
		t.Fatalf("recall not allowed: %+v", items)
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
