package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const doneReceipt = "Fixed the form.\n\n```json\n" +
	`{"receipt": {"status": "done", "summary": "Fixed the contact form", "changed": ["site/form.go", "PR #7"], ` +
	`"verified": ["tests pass", "the page loads"], "not_verified": ["Safari"], "decisions": ["kept the old field"], ` +
	`"needs_approval": "merge the PR", "lessons": ["run the linter first"]}}` + "\n```"

func TestParseReceipt(t *testing.T) {
	r, ok := parseReceipt(doneReceipt)
	if !ok || r.Status != "done" || r.Summary != "Fixed the contact form" || !slices.Equal(r.Changed, []string{"site/form.go", "PR #7"}) ||
		!slices.Equal(r.NeedsApproval, []string{"merge the PR"}) || !slices.Equal(r.NotVerified, []string{"Safari"}) {
		t.Fatalf("receipt %+v %v", r, ok)
	}
	for name, text := range map[string]string{
		"tag":       `Done. <receipt>{"status": "Partial", "summary": "half"}</receipt>`,
		"bare":      `{"receipt": {"status": "blocked"}}`,
		"no lang":   "```\n{\"status\": \"done\"}\n```",
		"last wins": "```json\n{\"status\": \"blocked\"}\n```\nthen\n```json\n{\"receipt\": {\"status\": \"partial\"}}\n```",
	} {
		r, ok := parseReceipt(text)
		if !ok {
			t.Errorf("%s: no receipt", name)
			continue
		}
		if name == "tag" && r.Status != "partial" || name == "last wins" && r.Status != "partial" {
			t.Errorf("%s: status %q", name, r.Status)
		}
	}
	for name, text := range map[string]string{
		"none":       "All done!",
		"bad status": "```json\n{\"receipt\": {\"status\": \"great\"}}\n```",
		"broken":     "```json\n{\"receipt\": {\"status\": \"done\"\n```",
		"other json": "```json\n{\"name\": \"x\"}\n```",
	} {
		if _, ok := parseReceipt(text); ok {
			t.Errorf("%s: read as a receipt", name)
		}
	}
}

func TestStripReceipt(t *testing.T) {
	if s := stripReceipt(doneReceipt); s != "Fixed the form." {
		t.Errorf("stripped %q", s)
	}
	keep := "Here is the config:\n```json\n{\"name\": \"x\"}\n```"
	if s := stripReceipt(keep); s != keep {
		t.Errorf("stripped other JSON: %q", s)
	}
	if s := stripReceipt(`{"receipt": {"status": "done"}}`); s != "" {
		t.Errorf("a message that is only a receipt: %q", s)
	}
}

func ev(seq int, typ string, data map[string]any) EventRow {
	return EventRow{Seq: seq, Time: time.Now(), Type: typ, Data: data}
}

// What the agent says is verified, eriphe-core reads against the checks
// the node ran and the diff it saw: nothing that failed comes out verified.
func TestCrossCheck(t *testing.T) {
	events := []EventRow{
		ev(1, "text", map[string]any{"text": "working"}),
		ev(2, "turn_end", map[string]any{"subtype": "success"}),
		ev(3, "text", map[string]any{"text": doneReceipt}),
		ev(4, "turn_end", map[string]any{"subtype": "success"}),
		ev(5, "check", map[string]any{"name": "build", "ok": true, "run": "go build ./..."}),
		ev(6, "check", map[string]any{"name": "tests", "ok": false, "run": "go test ./...", "exit_code": 1, "output": "FAIL x"}),
		ev(7, "diff", map[string]any{"files": []any{"site/other.go"}}),
	}
	cr := crossCheck(events)
	if cr == nil || cr.Status != "done" || cr.Turn != 4 {
		t.Fatalf("receipt %+v", cr)
	}
	if !slices.Equal(cr.Verified, []string{"build"}) {
		t.Errorf("verified %v, want only the check that passed", cr.Verified)
	}
	if !slices.Equal(cr.Unchecked, []string{"the page loads"}) {
		t.Errorf("unchecked %v", cr.Unchecked)
	}
	if !slices.Contains(cr.NotVerified, "tests") || !slices.Contains(cr.NotVerified, "Safari") {
		t.Errorf("not verified %v", cr.NotVerified)
	}
	kinds := map[string]bool{}
	for _, m := range cr.Mismatches {
		kinds[m.Kind] = true
	}
	for _, k := range []string{mmVerifiedFailed, mmDoneFailed, mmNotInDiff, mmUnreported} {
		if !kinds[k] {
			t.Errorf("no %s mismatch in %+v", k, cr.Mismatches)
		}
	}
	if !cr.Unsupported {
		t.Error("done with a failed check is an unsupported claim")
	}
	text := cr.brainText()
	for _, want := range []string{"statut done", "vérifié par eriphe-core : build", "affirmé sans contrôle : the page loads",
		"contrôle tests échoué (code 1) : FAIL x", "attend l'accord : merge the PR", "écarts"} {
		if !strings.Contains(text, want) {
			t.Errorf("brain text lacks %q:\n%s", want, text)
		}
	}
}

// A receipt that the checks and the diff back up has no mismatch.
func TestCrossCheckClean(t *testing.T) {
	receipt := "```json\n{\"receipt\": {\"status\": \"done\", \"changed\": [\"site/form.go\"], \"verified\": [\"tests pass\"]}}\n```"
	cr := crossCheck([]EventRow{
		ev(1, "text", map[string]any{"text": receipt}),
		ev(2, "turn_end", map[string]any{"subtype": "success"}),
		ev(3, "check", map[string]any{"name": "tests", "ok": true}),
		ev(4, "diff", map[string]any{"files": []any{"site/form.go"}}),
	})
	if cr.Unsupported || len(cr.Mismatches) > 0 || len(cr.Unchecked) > 0 || !slices.Equal(cr.Verified, []string{"tests"}) {
		t.Errorf("receipt %+v", cr)
	}
	if crossCheck([]EventRow{ev(1, "text", map[string]any{"text": "hi"})}) != nil {
		t.Error("a receipt before the first turn ended")
	}
	if cr := crossCheck([]EventRow{ev(1, "turn_end", map[string]any{"result": "All done"})}); !cr.missing() {
		t.Errorf("no receipt read as %+v", cr)
	}
}

func receiptCore(t *testing.T) *core {
	t.Helper()
	s, err := openStore(filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{User: "Sam", DefaultAgent: "small", Brain: &BrainConfig{},
		Providers: map[string]Provider{"p": {Kind: "claude-subscription", Secret: "/nonexistent"}},
		Agents:    map[string]Agent{"small": {Runtime: "claude-code", Provider: "p"}}}
	return newCore(cfg, s)
}

func lastEvent(t *testing.T, c *core) string {
	t.Helper()
	msgs, _ := c.store.undelivered()
	if len(msgs) == 0 {
		return ""
	}
	return msgs[len(msgs)-1].Text
}

// A turn without a receipt is asked for it once, without the brain; the
// brain then hears of the turn, with the receipt or the last message.
func TestReceiptRepair(t *testing.T) {
	c := receiptCore(t)
	c.store.insertTask(&TaskRow{ID: "t-1", Node: "pc", Agent: "small", Runtime: "claude-code", Prompt: "Fix it", State: stateIdle})
	c.store.addEvents("t-1", []EventRow{ev(1, "text", map[string]any{"text": "All done!"}), ev(2, "turn_end", map[string]any{"subtype": "success"})})
	c.notifyBrain("t-1", stateIdle)
	if msg := lastEvent(t, c); msg != "" {
		t.Fatalf("the brain heard %q before the receipt", msg)
	}
	items, _ := c.store.inbox("t-1")
	if len(items) != 1 || items[0].Data["text"] != receiptRepair || items[0].Data["skip_checks"] != true {
		t.Fatalf("inbox %+v", items)
	}
	if task, _ := c.store.task("t-1"); task.State != stateRunning {
		t.Errorf("state %s", task.State)
	}
	// Its answer has no receipt either: not asked twice in a row.
	c.store.ackInbox("t-1", items[0].Seq)
	c.store.addEvents("t-1", []EventRow{ev(3, "text", map[string]any{"text": "Sorry, all done."}), ev(4, "turn_end", map[string]any{"subtype": "success"})})
	c.notifyBrain("t-1", stateIdle)
	if msg := lastEvent(t, c); !strings.Contains(msg, "sans reçu lisible") || !strings.Contains(msg, "Sorry, all done.") {
		t.Errorf("brain event %q", msg)
	}
	if items, _ := c.store.inbox("t-1"); len(items) != 0 {
		t.Errorf("asked again: %+v", items)
	}
}

// With a receipt, the brain reads it instead of the agent's message.
func TestReceiptToBrain(t *testing.T) {
	c := receiptCore(t)
	c.store.insertTask(&TaskRow{ID: "t-1", Node: "pc", Agent: "small", Runtime: "claude-code", Prompt: "Fix it", State: stateIdle})
	c.store.addEvents("t-1", []EventRow{ev(1, "text", map[string]any{"text": doneReceipt}), ev(2, "turn_end", map[string]any{"subtype": "success"})})
	c.notifyBrain("t-1", stateIdle)
	msg := lastEvent(t, c)
	if !strings.Contains(msg, "Reçu (recoupé par eriphe-core) : statut done") || strings.Contains(msg, "```") {
		t.Errorf("brain event %q", msg)
	}
	if h := c.store.harness("t-1"); h.Receipt == nil || h.Receipt.Status != "done" {
		t.Errorf("kept %+v", h)
	}
	st, _ := c.status("t-1", 10)
	if st.LastText != "Fixed the form." || st.Receipt == nil || st.Receipt.Summary != "Fixed the contact form" {
		t.Errorf("status %q %+v", st.LastText, st.Receipt)
	}
}

// A failed turn is not asked for its receipt.
func TestNoRepairAfterFailure(t *testing.T) {
	c := receiptCore(t)
	c.store.insertTask(&TaskRow{ID: "t-1", Node: "pc", Agent: "small", Runtime: "claude-code", Prompt: "Fix it", State: stateIdle})
	c.store.addEvents("t-1", []EventRow{ev(1, "turn_end", map[string]any{"subtype": "success", "is_error": true, "result": "API Error: 500"})})
	c.notifyBrain("t-1", stateIdle)
	if items, _ := c.store.inbox("t-1"); len(items) != 0 {
		t.Errorf("asked: %+v", items)
	}
}

// The night's extraction reads a task's receipt rather than its last message.
func TestTranscriptReadsReceipt(t *testing.T) {
	c := receiptCore(t)
	c.store.insertTask(&TaskRow{ID: "t-1", Node: "pc", Agent: "small", Runtime: "claude-code", Prompt: "Fix it", State: stateStopped})
	c.store.addEvents("t-1", []EventRow{ev(1, "text", map[string]any{"text": doneReceipt}), ev(2, "turn_end", map[string]any{"subtype": "success"})})
	segs, err := c.transcript(time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil || len(segs) != 1 {
		t.Fatalf("segments %+v %v", segs, err)
	}
	for _, want := range []string{"Its receipt (task output, external): status done", "decisions: kept the old field",
		"lessons it proposes: run the linter first", "claimed, not checked: tests pass | the page loads"} {
		if !strings.Contains(segs[0].Text, want) {
			t.Errorf("segment lacks %q: %s", want, segs[0].Text)
		}
	}
}

// The drawer shows verified and not verified side by side, without the
// raw block, in French and in English.
func TestReceiptOnCard(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	tw.c.store.insertTask(&TaskRow{ID: "t-1", Node: "pc", Agent: "small", Runtime: "claude-code", Prompt: "Fix it", State: stateIdle})
	tw.c.store.addEvents("t-1", []EventRow{ev(1, "text", map[string]any{"text": doneReceipt}), ev(2, "turn_end", map[string]any{"subtype": "success"}),
		ev(3, "check", map[string]any{"name": "tests", "ok": false, "exit_code": 2, "output": "FAIL here"})})
	_, body, _ := tw.do("GET", "/tasks/t-1", nil, map[string]string{"HX-Request": "true"})
	for _, want := range []string{"Compte rendu", "Vérifié par Eriphe", "Non vérifié", "the page loads", "affirmé, sans contrôle",
		"échoué (code 2)", "FAIL here", "Affirmation non étayée", "La tâche se dit finie, mais le contrôle tests a échoué.", "merge the PR"} {
		if !strings.Contains(body, want) {
			t.Errorf("drawer lacks %q", want)
		}
	}
	if strings.Contains(body, `&#34;receipt&#34;`) || strings.Contains(body, "needs_approval") {
		t.Error("the drawer shows the raw receipt")
	}
	_, body, _ = tw.do("GET", "/", nil, nil)
	if !strings.Contains(body, `rc rc-done`) || !strings.Contains(body, "non vérifié(s)") {
		t.Error("the card has no receipt line")
	}
	if s := (mismatch{Kind: mmUnreported, N: 3}).Text("en"); s != "3 file(s) changed in the repository are not in the report." {
		t.Errorf("english %q", s)
	}
}
