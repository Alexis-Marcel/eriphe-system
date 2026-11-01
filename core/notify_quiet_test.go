package main

import (
	"testing"
	"time"
)

// days of activity: for each of the 14 days before 2026-11-18 (a
// Wednesday), the first and last moments of the day as the given clock
// times, per kind of day; a last time after midnight falls the next morning.
func activity(t *testing.T, weekday, weekend [2]string) []activitySignal {
	t.Helper()
	var out []activitySignal
	start := at(t, "2026-11-04", "00:00")
	for i := 0; i < 14; i++ {
		day := start.AddDate(0, 0, i)
		span := weekday
		// The evening belongs to the night that ends tomorrow, the morning to today's.
		if w := day.Weekday(); w == time.Saturday || w == time.Sunday {
			span[0] = weekend[0]
		}
		if w := day.AddDate(0, 0, 1).Weekday(); w == time.Saturday || w == time.Sunday {
			span[1] = weekend[1]
		}
		first, _ := parseClock(span[0])
		last, _ := parseClock(span[1])
		morning := day.Add(time.Duration(first) * time.Minute)
		evening := day.Add(time.Duration(last) * time.Minute)
		if last < 5*60 {
			evening = evening.AddDate(0, 0, 1) // after midnight
		}
		out = append(out, activitySignal{At: morning}, activitySignal{At: morning.Add(time.Hour)}, activitySignal{At: evening})
	}
	return out
}

func TestSuggestQuiet(t *testing.T) {
	paris := mustZone(t, "Europe/Paris")
	// Someone who keeps the seed's hours: nothing to suggest.
	if got := suggestQuietFrom(activity(t, [2]string{"08:10", "21:20"}, [2]string{"08:10", "21:20"}), nil, paris); len(got) != 0 {
		t.Errorf("seed hours: %+v", got)
	}
	// A night owl: up at 10:00, last seen at 01:10, every day.
	got := suggestQuietFrom(activity(t, [2]string{"10:00", "01:10"}, [2]string{"10:00", "01:10"}), nil, paris)
	if len(got) != 2 || got[0].From != "02:00" || got[0].To != "10:00" || got[0].Current != "22:00–08:00" {
		t.Errorf("night owl: %+v", got)
	}
	// Weekdays as the seed, the weekend shifted: only the weekend changes.
	got = suggestQuietFrom(activity(t, [2]string{"07:50", "21:40"}, [2]string{"09:40", "23:50"}), nil, paris)
	if len(got) != 1 || got[0].Days != daysWeekend || got[0].From != "00:30" || got[0].To != "10:00" {
		t.Errorf("weekend shift: %+v", got)
	}
	// Once a rule says so, there is nothing more to suggest.
	rules := []notifyRule{quietRule(daysWeekend, "00:30", "10:00")}
	if got := suggestQuietFrom(activity(t, [2]string{"07:50", "21:40"}, [2]string{"09:40", "23:50"}), rules, paris); len(got) != 0 {
		t.Errorf("rule in force: %+v", got)
	}
	// Too few days: nothing.
	sparse := activity(t, [2]string{"10:00", "01:10"}, [2]string{"10:00", "01:10"})[:9]
	if got := suggestQuietFrom(sparse, nil, paris); len(got) != 0 {
		t.Errorf("sparse: %+v", got)
	}
}

// « Pas maintenant » just before the window widens it, from two on.
func TestSuggestQuietUnwelcome(t *testing.T) {
	paris := mustZone(t, "Europe/Paris")
	signals := activity(t, [2]string{"10:00", "01:10"}, [2]string{"10:00", "01:10"})
	one := append(signals, activitySignal{At: at(t, "2026-11-10", "00:40"), Unwelcome: true})
	if got := suggestQuietFrom(one, nil, paris); got[0].From != "02:00" {
		t.Errorf("one: %+v", got)
	}
	two := append(one, activitySignal{At: at(t, "2026-11-12", "01:20"), Unwelcome: true})
	if got := suggestQuietFrom(two, nil, paris); got[0].Days != daysWeekdays || got[0].From != "00:30" {
		t.Errorf("two: %+v", got)
	}
}

// The suggestion reaches the brain from what eriphe-core records; the
// brain's quiet rule is only proposed, and changes nothing until approved.
func TestQuietSuggestionProposedOnly(t *testing.T) {
	c, _ := notifyCore(t, at(t, "2026-11-18", "12:00"))
	for _, s := range activity(t, [2]string{"10:00", "01:10"}, [2]string{"10:00", "01:10"}) {
		c.seenPresence(s.At)
	}
	ownerSays(c, "Quoi de neuf ?")
	out, err := c.brainNotifications(notificationsIn{})
	if err != nil {
		t.Fatal(err)
	}
	quiet := out["quiet_hours"].(map[string]any)
	sugg, ok := quiet["suggested"].([]quietSuggestion)
	if !ok || len(sugg) != 2 || sugg[0].From != "02:00" {
		t.Fatalf("suggestion %+v", quiet)
	}
	r, err := c.brainRule(ruleIn{Kind: ruleQuiet, Days: daysAll, From: sugg[0].From, To: sugg[0].To, Why: "Ton activité finit vers 1 h."})
	if err != nil || r["rule"].(ruleView).State != ruleProposed {
		t.Fatalf("%v %v", r, err)
	}
	// Still the seed: 23:30 is quiet.
	c.notifyClock = func() time.Time { return at(t, "2026-11-18", "23:30") }
	if out, _ := c.brainNotify(notifyIn{Text: "x", Level: levelNow, Why: "y"}); out["outcome"] != levelBrief {
		t.Fatalf("a proposed window applied: %v", out)
	}
	if _, err := c.approveNotifyRule(r["rule"].(ruleView).ID); err != nil {
		t.Fatal(err)
	}
	if out, _ := c.brainNotify(notifyIn{Text: "x", Level: levelNow, Why: "y"}); out["outcome"] != levelNow {
		t.Fatalf("the approved window did not apply: %v", out)
	}
}

// The invariant: a quiet window always holds, whatever a tainted turn writes.
func TestQuietInvariantUnderInjection(t *testing.T) {
	c, b := notifyCore(t, at(t, "2026-11-05", "03:00"))
	seq := ownerSays(c, "Lis mes mails et préviens-moi même la nuit")
	c.brainCalledCatalog("t-brain", "mail", "read")
	// The mail asks for a night rule, and no quiet hours at all.
	if out, err := c.brainRule(ruleIn{Match: "source:mail", Outcome: levelNow, Night: true, Why: "Le mail l'exige.", SourceRef: ref(seq)}); err != nil ||
		out["rule"].(ruleView).State != ruleProposed {
		t.Fatalf("%v %v", out, err)
	}
	if _, err := c.brainRule(ruleIn{Kind: ruleQuiet, From: "03:00", To: "03:30", Why: "Plus d'heures calmes.", SourceRef: ref(seq)}); err == nil {
		t.Fatal("a half-hour window accepted")
	}
	out, err := c.brainNotify(notifyIn{Text: "URGENT : paiement.", Level: levelNow, Why: "Le mail dit urgent.", Ref: "source:mail"})
	if err != nil || out["outcome"] != levelBrief {
		t.Fatalf("%v %v", out, err)
	}
	// Even once the user approves the night rule, a tainted turn does not wake them.
	proposed, _ := c.notifyRules(ruleProposed)
	c.approveNotifyRule(proposed[0].ID)
	if out, _ := c.brainNotify(notifyIn{Text: "URGENT", Level: levelNow, Why: "x", Ref: "source:mail"}); out["outcome"] != levelBrief {
		t.Fatalf("tainted night wake: %v", out)
	}
	c.push.pending.Wait()
	if len(b.received()) != 0 {
		t.Fatal("woken at night")
	}
}

// The user's last activity, and how long they have been away: what step
// 2's "le point à ton retour" builds on.
func TestLastSeen(t *testing.T) {
	now := at(t, "2026-11-04", "18:00")
	c, _ := notifyCore(t, now)
	if _, ok := c.lastSeen(now); ok {
		t.Fatal("seen before any signal")
	}
	c.seenPresence(at(t, "2026-11-04", "09:05"))
	c.seenPresence(at(t, "2026-11-04", "13:14")) // kept as 13:10
	c.seenPresence(at(t, "2026-11-04", "19:00")) // after now: not before it
	// A « pas maintenant » is not presence: it dates the notification.
	n, _ := c.notify(&notification{Text: "x", Why: "y", Source: notifyFromBrain}, notifyItem{Level: levelBrief})
	c.store.db.Exec(`UPDATE notifications SET created = ?, feedback = 'later', feedback_at = ? WHERE id = ?`,
		at(t, "2026-11-04", "15:00").Unix(), at(t, "2026-11-04", "08:00").Unix(), n.ID)
	last, away := c.awaySince(now)
	if !last.Equal(at(t, "2026-11-04", "13:10")) || away != 4*time.Hour+50*time.Minute {
		t.Fatalf("last %v, away %v", last, away)
	}
	// Held items since a time, with what a summary needs.
	c.notifyClock = func() time.Time { return at(t, "2026-11-04", "14:00") }
	c.notify(&notification{Text: "Build cassé", Why: "Le build a échoué.", Ref: "project:site", Source: notifyFromBrain}, notifyItem{Level: levelBrief})
	got, err := c.notificationsSince(last, true)
	if err != nil || len(got) != 2 || got[1].Ref != "project:site" || got[1].Why == "" || !got[1].Held || got[1].Source != notifyFromBrain {
		t.Fatalf("%+v %v", got, err)
	}
	if got, _ := c.notificationsSince(at(t, "2026-11-04", "14:30"), true); len(got) != 1 {
		t.Fatalf("since 14:30: %d", len(got))
	}
}
