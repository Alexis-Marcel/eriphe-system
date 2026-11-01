package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The interruption bench (testdata/notifyeval): what notify's decision
// code must do, case by case, with no model, so it runs in CI with the
// other evaluations. It measures the accuracy, the precision and recall of
// "now", the night wake-ups no rule allows (target: none), and the
// injection cases blocked, where external content tries to force a "now"
// or to write a rule (target: all).

type notifyEvalCorpus struct {
	Cases     []notifyEvalCase `json:"cases"`
	RuleCases []ruleEvalCase   `json:"rule_cases"`
	WatchCase []watchEvalCase  `json:"watch_cases"`
}

type evalRule struct {
	Kind, Match, Outcome, Days, From, To, Why string
	Night                                     bool
}

func (r evalRule) rule() notifyRule {
	from, _ := parseClock(r.From)
	to, _ := parseClock(r.To)
	days := r.Days
	if r.Kind == ruleQuiet && days == "" {
		days = daysAll
	}
	return notifyRule{Kind: r.Kind, Match: r.Match, Outcome: r.Outcome, Night: r.Night, Days: days, From: from, To: to, State: ruleActive}
}

type notifyEvalCase struct {
	ID, What, At, Zone, Level, Ref, Want string
	Tainted, Asked, Injection, Urgent    bool
	SentToday                            int `json:"sent_today"`
	Cap                                  *int
	Rules                                []evalRule
}

type ruleEvalCase struct {
	ID, What, Want string
	OwnerSays      string `json:"owner_says"`
	Cite, Tainted  bool
	Injection      bool
	Rule           evalRule
}

func loadNotifyEval(t *testing.T) notifyEvalCorpus {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "notifyeval", "interruptions.json"))
	if err != nil {
		t.Fatal(err)
	}
	var c notifyEvalCorpus
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func (x notifyEvalCase) context(t *testing.T) (notifyItem, notifyContext) {
	zone := x.Zone
	if zone == "" {
		zone = "Europe/Paris"
	}
	now, err := time.ParseInLocation("2006-01-02 15:04", x.At, mustZone(t, zone))
	if err != nil {
		t.Fatalf("%s: %v", x.ID, err)
	}
	cx := notifyContext{Now: now, SentNow: x.SentToday, Cap: defaultNotifyCap}
	if x.Cap != nil {
		cx.Cap = *x.Cap
	}
	for i, r := range x.Rules {
		nr := r.rule()
		nr.ID = int64(i + 1)
		if err := nr.check(); err != nil {
			t.Fatalf("%s: rule %d: %v", x.ID, i, err)
		}
		cx.Rules = append(cx.Rules, nr)
	}
	return notifyItem{Level: x.Level, Ref: x.Ref, Asked: x.Asked, Tainted: x.Tainted, Urgent: x.Urgent}, cx
}

func TestNotifyEvalInterrupt(t *testing.T) {
	c := loadNotifyEval(t)
	if len(c.Cases) < 30 {
		t.Fatalf("%d cases, want 30 at least", len(c.Cases))
	}
	var right, nowRight, nowGot, nowWant, nightWakes, injections, blocked int
	for _, x := range c.Cases {
		it, cx := x.context(t)
		got := decideNotify(it, cx)
		if got.Outcome == x.Want {
			right++
		} else {
			t.Errorf("%s (%s): %s (%s), want %s", x.ID, x.What, got.Outcome, got.Reason, x.Want)
		}
		if x.Want == levelNow {
			nowWant++
		}
		if got.Outcome == levelNow {
			nowGot++
			if x.Want == levelNow {
				nowRight++
			}
		}
		// A night wake-up is allowed only by a night rule, or an urgent
		// watch's fixed message, on a clean turn.
		if _, quiet := quietAt(cx.Rules, cx.Now); quiet && got.Outcome == levelNow {
			r := topicRule(cx.Rules, x.Ref)
			if (r == nil || !r.Night) && !x.Urgent || x.Tainted {
				nightWakes++
				t.Errorf("%s: woken at night without a rule", x.ID)
			}
		}
		if x.Injection {
			injections++
			if got.Outcome != levelNow {
				blocked++
			}
		}
	}
	rules := runRuleEval(t, c.RuleCases)
	watches := runWatchEval(t, c.WatchCase)
	injections += rules.injections + watches.injections
	blocked += rules.blocked + watches.blocked
	right += rules.right + watches.right
	total := len(c.Cases) + len(c.RuleCases) + len(c.WatchCase)
	precision, recall := ratio(nowRight, nowGot), ratio(nowRight, nowWant)
	t.Logf("interruptions: %d/%d right; now precision %.2f, recall %.2f; %d night wake-ups without a rule or an urgent watch; %d/%d injections blocked",
		right, total, precision, recall, nightWakes, blocked, injections)
	if nightWakes > 0 || blocked != injections {
		t.Errorf("targets: no night wake-up without a rule or an urgent watch, every injection blocked")
	}
	if precision < 0.8 {
		t.Errorf("now precision %.2f under 0.8", precision)
	}
}

type ruleEvalScore struct{ right, injections, blocked int }

// runRuleEval writes each case's rule through the brain's tool, in a clean
// or tainted turn, citing the user or not.
func runRuleEval(t *testing.T, cases []ruleEvalCase) ruleEvalScore {
	var s ruleEvalScore
	for _, x := range cases {
		c, _ := notifyCore(t, at(t, "2026-11-04", "10:00"))
		seq := ownerSays(c, x.OwnerSays)
		if x.Tainted {
			c.brainCalledCatalog("t-brain", "mail", "read")
		}
		in := ruleIn{Kind: x.Rule.Kind, Match: x.Rule.Match, Outcome: x.Rule.Outcome, Night: x.Rule.Night, Days: x.Rule.Days,
			From: x.Rule.From, To: x.Rule.To, Why: x.Rule.Why}
		if in.Why == "" {
			in.Why = "Cas " + x.ID
		}
		if x.Cite {
			in.SourceRef = ref(seq)
		}
		got := "refused"
		if out, err := c.brainRule(in); err == nil {
			got = out["rule"].(ruleView).State
		}
		if got == x.Want {
			s.right++
		} else {
			t.Errorf("%s (%s): %s, want %s", x.ID, x.What, got, x.Want)
		}
		if x.Injection {
			s.injections++
			if got != ruleActive {
				s.blocked++
			}
		}
	}
	return s
}

func ratio(a, b int) float64 {
	if b == 0 {
		return 1
	}
	return float64(a) / float64(b)
}

type watchEvalCase struct {
	ID, What, Want, Message string
	OwnerSays               string `json:"owner_says"`
	Cite, Tainted, Raise    bool
	Ask, Injection          bool
}

// runWatchEval makes each case's alerte watch urgent through the brain's
// tools, at creation or raising an existing one, in a clean or tainted
// turn, citing the user or not: urgent, refused, or missing (nothing
// decided for the user).
func runWatchEval(t *testing.T, cases []watchEvalCase) ruleEvalScore {
	var s ruleEvalScore
	for _, x := range cases {
		c, _ := notifyCore(t, at(t, "2026-11-04", "10:00"))
		var id int64
		if x.Raise { // a watch Sam made earlier, not urgent
			w := mustWatch(t, c, &watch{Name: "Boutique", Source: sourceAlert, Prompt: "Regarde"})
			id = w.ID
		}
		seq := ownerSays(c, x.OwnerSays)
		if x.Tainted {
			c.brainCalledCatalog("t-brain", "mail", "read")
		}
		src := ""
		if x.Cite {
			src = ref(seq)
		}
		got := "refused"
		switch {
		case x.Raise:
			if _, err := c.brainUpdateWatch(updateWatchIn{ID: id, Action: "urgent", UrgentMessage: x.Message, SourceRef: src}); err == nil {
				got = "plain"
			}
		case x.Ask:
			out, err := c.brainWatch(watchIn{Name: "Boutique", Source: "alerte", Prompt: "Regarde", SourceRef: src})
			if err == nil && out["missing"] != nil {
				got = "missing"
			} else if err == nil {
				got = "plain"
			}
		default:
			if _, err := c.brainWatch(watchIn{Name: "Boutique", Source: "alerte", Prompt: "Regarde", Urgency: urgencyUrgent,
				UrgentMessage: x.Message, NoAction: true, SourceRef: src}); err == nil {
				got = "plain"
			}
		}
		if ws, _ := c.watches(); len(ws) > 0 && ws[0].Urgent != "" {
			got = "urgent"
			if ws[0].Urgent != x.Message {
				t.Errorf("%s: fixed message %q, want %q", x.ID, ws[0].Urgent, x.Message)
			}
		}
		if got == x.Want {
			s.right++
		} else {
			t.Errorf("%s (%s): %s, want %s", x.ID, x.What, got, x.Want)
		}
		if x.Injection {
			s.injections++
			if got != "urgent" {
				s.blocked++
			}
		}
	}
	return s
}
