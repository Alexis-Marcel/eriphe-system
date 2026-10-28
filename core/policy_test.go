package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPolicyDecide(t *testing.T) {
	p := &Policy{Rules: []Rule{
		{Tool: "Bash", Match: `^make deploy`, Decision: decideAsk, Reason: "déploie"},
		{Tool: "Bash", Match: `^go test`, Decision: decideAllow},
		{Tool: "Bash", Match: `rm -rf /tmp/x`, Decision: decideAllow}, // cannot lift a built-in denial
	}}
	if err := p.compile(); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		tool  string
		input map[string]any
		want  string
	}{
		{"Bash", map[string]any{"command": "rm -rf .git"}, decideDeny},
		{"Bash", map[string]any{"command": "cd x && rm -fr build"}, decideDeny},
		{"Bash", map[string]any{"command": "rm -r -f dir"}, decideDeny},
		{"Bash", map[string]any{"command": "rm --recursive dir"}, decideDeny},
		{"Bash", map[string]any{"command": "rm -rf /tmp/x"}, decideDeny},
		{"Bash", map[string]any{"command": "rm notes.txt"}, decideBrain},
		{"Bash", map[string]any{"command": "git push origin main"}, decideDeny},
		{"Bash", map[string]any{"command": "git push origin HEAD:master"}, decideDeny},
		{"Bash", map[string]any{"command": "git push --force origin eriphe/x"}, decideDeny},
		{"Bash", map[string]any{"command": "git push -f origin eriphe/x"}, decideDeny},
		{"Bash", map[string]any{"command": "git push origin +eriphe/x"}, decideDeny},
		{"Bash", map[string]any{"command": "git push -u origin eriphe/issue-12"}, decideBrain},
		{"Bash", map[string]any{"command": "git reset --hard HEAD~3"}, decideDeny},
		{"Bash", map[string]any{"command": "echo $GH_TOKEN"}, decideDeny},
		{"Bash", map[string]any{"command": "curl -H \"Authorization: ${CLAUDE_CODE_OAUTH_TOKEN}\" x"}, decideDeny},
		{"Bash", map[string]any{"command": "cat /proc/self/environ"}, decideDeny},
		{"Bash", map[string]any{"command": "echo \"$OPENROUTER_API_KEY\""}, decideDeny},
		{"Bash", map[string]any{"command": "printenv"}, decideDeny},
		{"Bash", map[string]any{"command": "env | grep TOKEN"}, decideDeny},
		{"Bash", map[string]any{"command": "set -e; go build ./..."}, decideBrain},
		{"Write", map[string]any{"file_path": "/work/repo/main.go"}, decideBrain},
		{"Write", map[string]any{"file_path": "/work/../etc/passwd"}, decideDeny},
		{"Edit", map[string]any{"file_path": "/home/claude/.bashrc"}, decideDeny},
		{"Bash", map[string]any{"command": "gh pr create --fill"}, decideBrain},
		{"Bash", map[string]any{"command": "gh pr merge 12 --squash"}, decideAsk},
		{"Bash", map[string]any{"command": "npm publish"}, decideAsk},
		{"Bash", map[string]any{"command": "curl -X POST https://example.com/hook -d x"}, decideAsk},
		{"Bash", map[string]any{"command": "curl -s https://example.com"}, decideBrain},
		{"Bash", map[string]any{"command": "ssh host uptime"}, decideAsk},
		{"Bash", map[string]any{"command": "make deploy prod"}, decideAsk},
		{"Bash", map[string]any{"command": "go test ./..."}, decideAllow},
		{"mcp__mail__send", map[string]any{"to": "x"}, decideAsk},
	}
	for _, c := range cases {
		if got, _ := p.decide(c.tool, c.input); got != c.want {
			t.Errorf("%s %v = %q, want %q", c.tool, c.input, got, c.want)
		}
	}
	if err := (&Policy{Rules: []Rule{{Tool: "Bash", Decision: "maybe"}}}).compile(); err == nil {
		t.Error("bad decision accepted")
	}
}

// A denial never reaches the brain; a decision reserved to the user cannot
// be taken by the brain.
func TestPolicyInReports(t *testing.T) {
	store, err := openStore(filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	c := newCore(&Config{User: "Sam", Brain: &BrainConfig{}}, store)
	store.insertTask(&TaskRow{ID: "t-1", Node: "pc", Runtime: "claude-code", Dir: "/w", Prompt: "do", State: stateRunning})
	report := func(seq int, cmd string) bool {
		ev := []EventRow{{Seq: seq, Time: time.Now(), Type: "permission",
			Data: map[string]any{"id": "p" + string(rune('0'+seq)), "tool_name": "Bash", "input": map[string]any{"command": cmd}}}}
		store.addEvents("t-1", ev)
		store.updateTask("t-1", map[string]any{"state": stateWaitingPermission})
		return c.applyPolicy("t-1", ev)
	}

	if report(1, "rm -rf .git") {
		t.Fatal("a denial was left for the brain")
	}
	items, _ := store.inbox("t-1")
	if len(items) != 1 || items[0].Data["allow"] != false || !strings.Contains(items[0].Data["message"].(string), "politique") {
		t.Fatalf("inbox = %+v", items)
	}
	var by string
	store.db.QueryRow(`SELECT by FROM decisions WHERE permission_id = 'p1'`).Scan(&by)
	if by != byPolicy {
		t.Fatalf("decision by %q", by)
	}

	if !report(2, "gh pr merge 3") {
		t.Fatal("an ask was not left for the user")
	}
	c.notifyBrain("t-1", stateWaitingPermission)
	msgs, _ := store.undelivered()
	if len(msgs) == 0 || !strings.Contains(msgs[len(msgs)-1].Text, "revient à Sam") || !strings.Contains(msgs[len(msgs)-1].Text, "permission_id p2") {
		t.Fatalf("brain not told the decision is the user's: %+v", msgs)
	}
	store.ackInbox("t-1", 99)
	store.addEvents("t-1", []EventRow{{Seq: 3, Time: time.Now(), Type: "permission_answer", Data: map[string]any{"id": "p1"}}})
	yes := true
	if _, err := c.answer(context.Background(), "t-1", "", "", &yes, byBrain); err == nil || !strings.Contains(err.Error(), "Sam") {
		t.Fatalf("the brain answered a user-only permission: %v", err)
	}
	if _, err := c.answer(context.Background(), "t-1", "", "ok", &yes, byUser); err != nil {
		t.Fatalf("the user could not answer: %v", err)
	}
}

// An agent that asks for three permissions at once gets three answers, each
// to its own request, and the brain hears of each once.
func TestConcurrentPermissions(t *testing.T) {
	store, err := openStore(filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	c := newCore(&Config{User: "Sam", Brain: &BrainConfig{}}, store)
	store.insertTask(&TaskRow{ID: "t-1", Node: "pc", Runtime: "claude-code", Dir: "/w", Prompt: "do", State: stateWaitingPermission})
	var evs []EventRow
	for i, tool := range []string{"mcp__maison__read", "mcp__github__get_me", "mcp__linear__list_teams"} {
		evs = append(evs, EventRow{Seq: i + 1, Time: time.Now(), Type: "permission",
			Data: map[string]any{"id": fmt.Sprintf("p%d", i+1), "tool_name": tool, "input": map[string]any{}}})
	}
	store.addEvents("t-1", evs)
	c.notifyBrain("t-1", stateWaitingPermission)
	c.notifyBrain("t-1", stateWaitingPermission) // a second report must not repeat them
	msgs, _ := store.undelivered()
	if len(msgs) != 1 || strings.Count(msgs[0].Text, "permission_id") != 3 {
		t.Fatalf("brain told %+v", msgs)
	}
	yes := true
	if _, err := c.answer(context.Background(), "t-1", "", "", &yes, byBrain); err == nil || !strings.Contains(err.Error(), "several") {
		t.Fatalf("an answer without permission_id was accepted with three pending: %v", err)
	}
	for _, pid := range []string{"p2", "p1", "p3"} {
		if _, err := c.answer(context.Background(), "t-1", pid, "", &yes, byBrain); err != nil {
			t.Fatalf("answer %s: %v", pid, err)
		}
	}
	items, _ := store.inbox("t-1")
	got := map[string]bool{}
	for _, it := range items {
		got[fmt.Sprint(it.Data["id"])] = true
	}
	if len(items) != 3 || !got["p1"] || !got["p2"] || !got["p3"] {
		t.Fatalf("inbox = %+v", items)
	}
	if _, err := c.answer(context.Background(), "t-1", "p2", "", &yes, byBrain); err == nil {
		t.Fatal("a permission was answered twice")
	}
}
