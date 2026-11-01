package main

import (
	"strings"
	"testing"
	"time"
)

// statsFixture: four finished tasks. t-a is done with its checks green the
// first time; t-b is done after a check went back; t-c says done while a
// check failed; t-d failed on its account.
func statsFixture(t *testing.T, c *core) {
	t.Helper()
	s := c.store
	turn := func(seq int, tokens, cost float64) EventRow {
		return ev(seq, "turn_end", map[string]any{"subtype": "success", "cost_usd": cost,
			"usage": map[string]any{"input_tokens": tokens, "output_tokens": 0, "cache_read_tokens": 99999}})
	}
	for _, id := range []string{"t-a", "t-b", "t-c", "t-d"} {
		s.insertTask(&TaskRow{ID: id, Node: "pc", Agent: "small", Runtime: "claude-code", Prompt: "Fix " + id, State: stateStopped})
	}
	s.addEvents("t-a", []EventRow{turn(1, 1000, 0.10), ev(2, "check", map[string]any{"name": "tests", "ok": true})})
	s.setHarness("t-a", map[string]any{"receipt": &checkedReceipt{Status: "done", Verified: []string{"tests"},
		Checks: []checkResult{{Name: "tests", OK: true}}}})
	s.addEvents("t-b", []EventRow{turn(1, 2000, 0.20), ev(2, "retry", map[string]any{"reason": "check"}), turn(3, 1000, 0.10)})
	s.setHarness("t-b", map[string]any{"receipt": &checkedReceipt{Status: "done", Unchecked: []string{"it works on Safari"}},
		"failures": `["check"]`})
	s.addEvents("t-c", []EventRow{turn(1, 500, 0.05)})
	s.setHarness("t-c", map[string]any{"receipt": &checkedReceipt{Status: "done", Unsupported: true,
		Checks: []checkResult{{Name: "tests", OK: false}}}, "failures": `["check","transient"]`})
	s.setHarness("t-d", map[string]any{"failures": `["account"]`})
}

func TestTaskStats(t *testing.T) {
	c := receiptCore(t)
	statsFixture(t, c)
	st := c.store.stats(time.Now().Add(-time.Hour))
	if st.Finished != 4 || st.Receipts != 3 || st.Accepted != 2 || st.FirstPass != 1 || st.Unsupported != 1 || st.Unchecked != 1 {
		t.Fatalf("stats %+v", st)
	}
	// The accepted ones cost 0.40 $ and 4000 tokens (cache reads aside): 0.20 $ and 2000 each.
	if st.Costed != 2 || st.TokensPerAccepted() != 2000 || st.CostPerAccepted() < 0.199 || st.CostPerAccepted() > 0.201 {
		t.Errorf("cost %+v", st)
	}
	if st.FailedTasks != 3 || len(st.Failures) != 3 || st.Failures[0] != (classCount{classCheck, 2}) {
		t.Errorf("failures %+v", st.Failures)
	}
	if st.Rate(1, 3) != 33 || st.Rate(1, 0) != 0 {
		t.Error("rate")
	}
	if old := c.store.stats(time.Now().Add(time.Hour)); old.Finished != 0 {
		t.Errorf("out of the window: %+v", old)
	}
}

func TestMoneyAndTokens(t *testing.T) {
	for _, c := range []struct {
		lang string
		usd  float64
		want string
	}{{"fr", 0.4213, "0,42 $"}, {"en", 0.4213, "$0.42"}, {"en", 0.004, "$0.004"}} {
		if got := money(c.lang, c.usd); got != c.want {
			t.Errorf("money(%s, %v) = %q", c.lang, c.usd, got)
		}
	}
	for n, want := range map[int64]string{950: "950", 12400: "12 k", 1_250_000: "1,2 M"} {
		if got := tokenCount("fr", n); got != want {
			t.Errorf("tokenCount(%d) = %q", n, got)
		}
	}
}

// The side panel shows the measures; a task's drawer its usage.
func TestStatsOnPanel(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	statsFixture(t, tw.c)
	_, body, _ := tw.do("GET", "/", nil, nil)
	for _, want := range []string{"Fiabilité des tâches · 30 jours", "Acceptées", "50 %", "2/4", "Du premier coup", "1/4",
		"Affirmations non étayées", "1 sans contrôle", "0,20 $", "2 k jetons", "contrôle 2", "compte 1", "passagers 1"} {
		if !strings.Contains(body, want) {
			t.Errorf("panel lacks %q", want)
		}
	}
	_, body, _ = tw.do("GET", "/tasks/t-b", nil, map[string]string{"HX-Request": "true"})
	if !strings.Contains(body, "3 k jetons · 0,30 $") {
		t.Error("the drawer has no usage")
	}
}
