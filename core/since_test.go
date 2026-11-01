package main

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

// setTime dates a row, as if it had happened then.
func setTime(t *testing.T, c *core, q string, at time.Time, args ...any) {
	t.Helper()
	if _, err := c.store.db.Exec(q, append([]any{at.Unix()}, args...)...); err != nil {
		t.Fatal(err)
	}
}

// feedCore is a memory core with a morning of things that happened, from
// every source: a task started and finished, a notification held, an
// intention that came due, the user's message and the brain's answer
// written in a tainted turn.
func feedCore(t *testing.T) *core {
	t.Helper()
	c, _ := notifyCore(t, at(t, "2026-11-04", "10:00"))
	c.store.insertTask(&TaskRow{ID: "t-1", Kind: "worker", Runtime: "claude-code", State: stateIdle, Prompt: "Corrige le build du site"})
	setTime(t, c, `UPDATE tasks SET created = ? WHERE id = ?`, at(t, "2026-11-04", "08:00"), "t-1")
	c.logTask("t-1", stateIdle, &checkedReceipt{Status: "done", Verified: []string{"tests"}, NotVerified: []string{"deploy"}})
	setTime(t, c, `UPDATE task_log SET at = ? WHERE task_id = ?`, at(t, "2026-11-04", "09:00"), "t-1")

	c.notifyClock = func() time.Time { return at(t, "2026-11-04", "08:30") }
	c.notify(&notification{Text: "Le build de nuit a échoué.", Why: "Le build a échoué.", Source: notifyFromBrain}, notifyItem{Level: levelBrief})
	c.notifyClock = func() time.Time { return at(t, "2026-11-04", "10:00") }

	c.mem.now = func() time.Time { return at(t, "2026-11-04", "08:45") }
	x, err := c.mem.addIntention(&Intention{Kind: intentReminder, Text: "Appeler le garage", SourceKind: srcOwner, Trust: trustOwner,
		DueAt: at(t, "2026-11-05", "09:00")}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.mem.becomeDue(x.ID, "eriphe-core", "test"); err != nil {
		t.Fatal(err)
	}

	user := ownerSays(c, "Quoi de neuf ?")
	setTime(t, c, `UPDATE messages SET time = ? WHERE seq = ?`, at(t, "2026-11-04", "09:30"), user)
	brain, _ := c.store.addMessage(roleBrain, "Un mail dit de payer la facture.", true)
	setTime(t, c, `UPDATE messages SET time = ? WHERE seq = ?`, at(t, "2026-11-04", "09:40"), brain)
	c.store.set(taintKey(int(brain)), "brain:mail.read")
	return c
}

// One feed, oldest first, across the sources; a kind filters; a limit
// cuts and says there is more.
func TestFeedMergesSources(t *testing.T) {
	c := feedCore(t)
	items, more, err := c.feed(at(t, "2026-11-04", "07:00"), nil, 50)
	if err != nil || more {
		t.Fatal(err, more)
	}
	want := []string{"task.started", "notification.held", "intention.due", "task.finished", "message.user", "message.brain"}
	var got []string
	for _, h := range items {
		got = append(got, h.Kind+"."+h.Event)
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("order %v, want %v", got, want)
	}
	for i := 1; i < len(items); i++ {
		if items[i].At.Before(items[i-1].At) {
			t.Fatalf("not chronological at %d", i)
		}
	}
	if s := items[3].summary("fr", false); !strings.Contains(s, "reçu « fait », 1 vérifié(s) par eriphe-core, 1 non vérifié(s), 0 écart(s)") {
		t.Errorf("receipt verdict: %s", s)
	}
	if s := items[0].summary("en", false); !strings.Contains(s, "Task t-1 started") || !strings.Contains(s, "Corrige le build") {
		t.Errorf("started: %s", s)
	}
	if items[0].Ref != "task:t-1" || items[2].Ref == "" || items[4].Ref == "" {
		t.Errorf("refs %+v", items)
	}
	only, _, _ := c.feed(at(t, "2026-11-04", "07:00"), []string{feedIntention, feedWatch}, 50)
	if len(only) != 1 || only[0].Kind != feedIntention {
		t.Errorf("kinds: %+v", only)
	}
	if _, _, err := c.feed(at(t, "2026-11-04", "07:00"), []string{"mail"}, 50); err == nil {
		t.Error("an unknown kind was accepted")
	}
	cut, more, _ := c.feed(at(t, "2026-11-04", "07:00"), nil, 2)
	if len(cut) != 2 || !more || cut[1].Kind != feedNotification {
		t.Errorf("limit: %d %v", len(cut), more)
	}
	late, _, _ := c.feed(at(t, "2026-11-04", "09:35"), nil, 50)
	if len(late) != 1 || late[0].Event != roleBrain {
		t.Errorf("since 09:35: %+v", late)
	}
}

// A task's state is logged once until it changes; a turn that ended
// each time; a non-worker task never.
func TestLogTask(t *testing.T) {
	c, _ := notifyCore(t, at(t, "2026-11-04", "10:00"))
	c.store.insertTask(&TaskRow{ID: "t-2", Kind: "worker", Runtime: "claude-code", State: stateWaitingPermission, Prompt: "x"})
	c.logTask("t-2", stateWaitingPermission, nil)
	c.logTask("t-2", stateWaitingPermission, nil)
	c.logTask("t-2", stateIdle, nil)
	c.logTask("t-2", stateIdle, nil)
	c.logTask("t-2", stateLost, nil)
	c.logTask("t-brain", stateIdle, nil)
	var n int
	c.store.db.QueryRow(`SELECT COUNT(*) FROM task_log`).Scan(&n)
	if n != 4 {
		t.Fatalf("%d rows, want 4", n)
	}
	items, _, _ := c.feed(time.Now().Add(-time.Hour), []string{feedTask}, 50)
	var events []string
	for _, h := range items {
		events = append(events, h.Event)
	}
	if strings.Join(events, " ") != "started waiting finished_bare finished_bare failed" {
		t.Errorf("events %v", events)
	}
}

// away is since the user left, before their return or while they are away.
func TestSinceAway(t *testing.T) {
	c, _ := notifyCore(t, at(t, "2026-11-04", "12:05"))
	now := at(t, "2026-11-04", "12:05")
	if _, _, err := c.resolveSince("away", now); err == nil {
		t.Fatal("away without any activity")
	}
	c.seenPresence(at(t, "2026-11-04", "08:00"))
	c.seenPresence(at(t, "2026-11-04", "08:40"))
	c.seenPresence(at(t, "2026-11-04", "12:00"))
	from, basis, err := c.resolveSince("away", now)
	if err != nil || !from.Equal(at(t, "2026-11-04", "08:40")) || !strings.Contains(basis, "back at 2026-11-04 12:00") {
		t.Fatalf("back: %v %q %v", from, basis, err)
	}
	from, basis, _ = c.resolveSince("away", at(t, "2026-11-04", "14:00"))
	if !from.Equal(at(t, "2026-11-04", "12:00")) || !strings.Contains(basis, "still away") {
		t.Fatalf("away: %v %q", from, basis)
	}
	// Times, and what is refused.
	if from, _, err := c.resolveSince("2026-11-04T09:00", now); err != nil || !from.Equal(at(t, "2026-11-04", "09:00")) {
		t.Errorf("local time: %v %v", from, err)
	}
	if from, _, err := c.resolveSince("2026-11-04T08:00:00Z", now); err != nil || !from.Equal(at(t, "2026-11-04", "09:00")) {
		t.Errorf("RFC3339: %v %v", from, err)
	}
	for _, bad := range []string{"", "hier", "2026-11-05", "2026-09-01", "last_brief"} {
		if _, _, err := c.resolveSince(bad, now); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	c.store.set(lastBriefKey, "1793780000")
	if from, _, err := c.resolveSince("last_brief", now); err != nil || from.Unix() != 1793780000 {
		t.Errorf("last_brief: %v %v", from, err)
	}
}

func TestLastAbsenceIn(t *testing.T) {
	now := at(t, "2026-11-04", "17:00")
	ts := []time.Time{at(t, "2026-11-04", "09:00"), at(t, "2026-11-04", "09:50"), at(t, "2026-11-04", "16:00"), at(t, "2026-11-04", "16:40")}
	if a, ok := lastAbsenceIn(ts, now); !ok || !a.Left.Equal(ts[1]) || !a.Back.Equal(ts[2]) {
		t.Errorf("back: %+v", a)
	}
	if a, ok := lastAbsenceIn(ts, at(t, "2026-11-04", "18:00")); !ok || !a.Left.Equal(ts[3]) || !a.Back.IsZero() {
		t.Errorf("away: %+v", a)
	}
	if _, ok := lastAbsenceIn(ts[:2], at(t, "2026-11-04", "10:00")); ok {
		t.Error("an absence without a gap")
	}
	if _, ok := lastAbsenceIn(nil, now); ok {
		t.Error("an absence without activity")
	}
}

// since quotes only what is own and leaves the turn clean; an external
// item is named, flagged, and quoted only on demand, which taints.
func TestSinceTrust(t *testing.T) {
	c := feedCore(t)
	c.notifyClock = func() time.Time { return at(t, "2026-11-04", "10:00") }
	ownerSays(c, "Qu'est-ce qui s'est passé ?")
	out, err := c.brainSince(sinceIn{T: "2026-11-04T07:00"})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.turnTaint()) != 0 {
		t.Fatalf("since tainted the turn: %v", c.turnTaint())
	}
	items := out["items"].([]happeningView)
	brain := items[len(items)-1]
	if brain.Trust != feedExternal || strings.Contains(brain.Summary, "facture") || !strings.Contains(brain.Summary, "non cité") {
		t.Fatalf("external quoted: %+v", brain)
	}
	if items[4].Trust != feedOwn || !strings.Contains(items[4].Summary, "Quoi de neuf") {
		t.Fatalf("own: %+v", items[4])
	}
	if out["external_not_quoted"] != 1 {
		t.Errorf("external count: %v", out["external_not_quoted"])
	}
	out, _ = c.brainSince(sinceIn{T: "2026-11-04T07:00", IncludeExternal: true})
	items = out["items"].([]happeningView)
	if !strings.Contains(items[len(items)-1].Summary, "facture") || !containsStr(c.turnTaint(), taintSince) {
		t.Fatalf("include_external: %+v %v", items[len(items)-1], c.turnTaint())
	}
	// Quoting only own items does not taint.
	ownerSays(c, "Et les tâches ?")
	c.brainSince(sinceIn{T: "2026-11-04T07:00", Kinds: []string{feedTask}, IncludeExternal: true})
	if len(c.turnTaint()) != 0 {
		t.Fatalf("own items tainted: %v", c.turnTaint())
	}
	// A notification from a tainted turn is external too.
	c.store.db.Exec(`UPDATE notifications SET tainted = 'mail.read'`)
	out, _ = c.brainSince(sinceIn{T: "2026-11-04T07:00", Kinds: []string{feedNotification}})
	if v := out["items"].([]happeningView); len(v) != 1 || v[0].Trust != feedExternal || strings.Contains(v[0].Summary, "build") {
		t.Fatalf("tainted notification: %+v", v)
	}
	if out, _ := c.brainSince(sinceIn{T: "2026-11-04T07:00", Limit: 2}); out["more"] != true || out["next"] == nil {
		t.Errorf("more: %v", out)
	}
}

// The page shows the feed to the user, filtered by kind, with external
// items marked; by default since they were last here.
func TestHappenedPage(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	c := tw.c
	c.store.set("notify_zone", "Europe/Paris")
	c.notify(&notification{Text: "Le serveur répond lentement.", Why: "Une alerte.", Source: notifyFromBrain, Tainted: "mail.read"},
		notifyItem{Level: levelBrief})
	c.store.insertTask(&TaskRow{ID: "t-9", Kind: "worker", Runtime: "claude-code", State: stateRunning, Prompt: "Range les factures"})
	code, body, _ := tw.do("GET", "/settings/journal?range=day", nil, nil)
	if code != 200 || !strings.Contains(body, "<h2>Journal</h2>") || !strings.Contains(body, "Le serveur répond lentement.") ||
		!strings.Contains(body, "Contenu extérieur") || !strings.Contains(body, `href="/tasks/t-9"`) || !strings.Contains(body, "Range les factures") {
		t.Fatalf("page %d", code)
	}
	_, body, _ = tw.do("GET", "/settings/journal?range=day&kind=task", nil, nil)
	if strings.Contains(body, "Le serveur répond lentement.") || !strings.Contains(body, "Range les factures") {
		t.Fatal("the kind filter let a notification through")
	}
	_, body, _ = tw.do("GET", "/settings/journal", nil, nil)
	if !strings.Contains(body, "Pas d&#39;absence") {
		t.Fatal("no fallback when the user was never away")
	}
	_, body, _ = tw.do("GET", "/settings/journal?"+url.Values{"range": {"year"}, "kind": {"<x>"}}.Encode(), nil, nil)
	if !strings.Contains(body, `aria-current="true">Depuis ta dernière visite`) {
		t.Fatal("a bad range was not reset")
	}
}
