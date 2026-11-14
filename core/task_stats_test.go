package main

import (
	"strings"
	"testing"
	"time"
)

// statsFixture: four finished tasks. t-a is done with its checks green the
// first time; t-b is done after a check went back; t-c says done while a
// check failed; t-d failed on its account. They run Opus; their turns'
// cost_usd is Claude Code's session total so far, never summed.
func statsFixture(t *testing.T, c *core) {
	t.Helper()
	s := c.store
	turn := func(seq int, tokens, total float64) EventRow {
		return ev(seq, "turn_end", map[string]any{"subtype": "success", "cost_usd": total,
			"usage": map[string]any{"input_tokens": tokens, "output_tokens": 0, "cache_read_tokens": 100000}})
	}
	for _, id := range []string{"t-a", "t-b", "t-c", "t-d"} {
		s.insertTask(&TaskRow{ID: id, Node: "pc", Agent: "small", Runtime: "claude-code", Model: "opus", Prompt: "Fix " + id, State: stateStopped})
	}
	s.addEvents("t-a", []EventRow{turn(1, 1000, 50.10), ev(2, "check", map[string]any{"name": "tests", "ok": true})})
	s.setHarness("t-a", map[string]any{"receipt": &checkedReceipt{Status: "done", Verified: []string{"tests"},
		Checks: []checkResult{{Name: "tests", OK: true}}}})
	s.addEvents("t-b", []EventRow{turn(1, 2000, 50.20), ev(2, "retry", map[string]any{"reason": "check"}), turn(3, 1000, 50.30)})
	s.setHarness("t-b", map[string]any{"receipt": &checkedReceipt{Status: "done", Unchecked: []string{"it works on Safari"}},
		"failures": `["check"]`})
	s.addEvents("t-c", []EventRow{turn(1, 500, 50.35)})
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
	// The accepted ones used 4000 input tokens and 300 k cache reads at
	// Opus's price, 0.016 + 0.06 $: 0.038 $ and 2000 tokens (cache reads
	// aside) each.
	if st.Costed != 2 || st.TokensPerAccepted() != 2000 || st.CostPerAccepted() < 0.0379 || st.CostPerAccepted() > 0.0381 {
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

// A turn's cost comes from its tokens at its model's price.
func TestTurnCost(t *testing.T) {
	tok := turnTokens{In: 1_000_000, Out: 1_000_000, CacheRead: 1_000_000, CacheWrite: 1_000_000}
	for model, want := range map[string]float64{"opus": 32.2, "claude-opus-5-5": 32.2, "Sonnet": 16.1, "claude-haiku-5-5": 8.1} {
		p, ok := priceOf(model)
		if got := tok.cost(p); !ok || got < want-1e-9 || got > want+1e-9 {
			t.Errorf("%s: %v, %v", model, got, ok)
		}
	}
	if _, ok := priceOf("mistral-large"); ok {
		t.Error("a model without a price has one")
	}
	if _, ok := priceOf(""); ok {
		t.Error("no model has a price")
	}
}

// The cost by day and role sums the turns of every task, by its kind;
// a model without a price costs nothing known.
func TestSpend(t *testing.T) {
	c := receiptCore(t)
	s := c.store
	turn := func(seq int, at time.Time, in, out int) EventRow {
		return EventRow{Seq: seq, Time: at, Type: "turn_end", Data: map[string]any{"cost_usd": 999.0,
			"usage": map[string]any{"input_tokens": in, "output_tokens": out, "cache_read_tokens": 0, "cache_write_tokens": 0}}}
	}
	now := time.Now()
	yesterday := now.AddDate(0, 0, -1)
	s.insertTask(&TaskRow{ID: "w", Node: "pc", Runtime: "claude-code", Model: "sonnet", Kind: "worker", State: stateStopped})
	s.insertTask(&TaskRow{ID: "b", Node: "pc", Runtime: "claude-code", Model: "opus", Kind: "brain", State: stateIdle})
	s.insertTask(&TaskRow{ID: "x", Node: "pc", Runtime: "opencode", Model: "mistral/large", Kind: "worker", State: stateStopped})
	s.addEvents("w", []EventRow{turn(1, now, 1_000_000, 0), turn(2, yesterday, 0, 100_000)})
	s.addEvents("b", []EventRow{turn(1, now, 500_000, 50_000), turn(2, now.AddDate(0, 0, -30), 1_000_000, 0)})
	s.addEvents("x", []EventRow{turn(1, now, 1_000_000, 1_000_000)})
	days, roles := s.spend(now.AddDate(0, 0, -spendDays))
	if len(roles) != 2 || roles[0] != "brain" || roles[1] != "worker" {
		t.Fatalf("roles %v", roles)
	}
	if len(days) != 2 || days[0].Day != now.Format("2006-01-02") || days[1].Day != yesterday.Format("2006-01-02") {
		t.Fatalf("days %+v", days)
	}
	near := func(a, b float64) bool { return a > b-1e-9 && a < b+1e-9 }
	// Today: the worker 1 M Sonnet input (2 $), the brain 0.5 M Opus input
	// and 50 k output (2 + 1 $).
	if !near(days[0].Cost("worker"), 2) || !near(days[0].Cost("brain"), 3) || !near(days[0].Total, 5) {
		t.Errorf("today %+v", days[0])
	}
	if !near(days[1].Cost("worker"), 1) || days[1].Cost("brain") != 0 || !near(days[1].Total, 1) {
		t.Errorf("yesterday %+v", days[1])
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

// The side panel shows the measures and the cost by role and day; a
// task's drawer its usage.
func TestStatsOnPanel(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	statsFixture(t, tw.c)
	// The subject cards the tasks started with, two judged, one used.
	for _, used := range []string{cardWrote, cardUnused, ""} {
		tw.c.store.db.Exec(`INSERT INTO card_pushes (at, subject, place, chars, who, used) VALUES (?, 'project:site', ?, 500, 't-a', ?)`,
			time.Now().Unix(), placeTask, used)
	}
	_, body, _ := tw.do("GET", convoPath, nil, nil)
	for _, want := range []string{"Fiabilité des tâches · 30 jours", "Acceptées", "50 %", "2/4", "Du premier coup", "1/4",
		`<a href="/settings/subjects">Fiches de sujet utilisées</a></dt><dd><strong>50 %</strong> <span class="muted small">1/2</span>`,
		"Affirmations non étayées", "1 sans contrôle", "0,04 $", "2 k jetons", "contrôle 2", "compte 1", "passagers 1",
		"Coût par rôle · 7 jours", ">worker</th>", "0,10 $"} {
		if !strings.Contains(body, want) {
			t.Errorf("panel lacks %q", want)
		}
	}
	_, body, _ = tw.do("GET", "/tasks/t-b", nil, map[string]string{"HX-Request": "true"})
	if !strings.Contains(body, "3 k jetons · 0,05 $") {
		t.Error("the drawer has no usage")
	}
}
