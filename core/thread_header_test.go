package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
)

// sinceOf is what a thread's data says changed since the user's visit
// before the last one (/thread/since), and its summary (/thread/summary);
// the frame draws neither: what is composed binds them.
func sinceOf(t *testing.T, c *core, id int64) ([]string, string) {
	t.Helper()
	s, data, _ := threadSurfaceOf(t, c, id)
	for _, drawn := range []string{"thread_since", "thread_fact_", "thread_where", "thread_summary"} {
		if strings.Contains(string(s.Components), `"id":"`+drawn) {
			t.Fatalf("the frame draws %s: %s", drawn, s.Components)
		}
	}
	var lines []string
	list, _ := data["since"].([]any)
	for _, l := range list {
		lines = append(lines, l.(string))
	}
	sum, _ := data["summary"].(string)
	return lines, sum
}

// Opening a thread's surface moves its visits: its data tells what changed
// since the visit before, so the one that opens it still sees what was
// new; never visited, or back with nothing new, nothing. Opened again
// within threadVisitSame, the same visit.
func TestThreadHeaderSinceVisit(t *testing.T) {
	c := startCore(t)
	th, err := c.openThread(threadIn{Title: "Site", Goal: "The contact page is online.", Alias: "project:site"}, nil, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	c.store.db.Exec(`UPDATE threads SET created = ? WHERE id = ?`, now.Add(-5*time.Hour).Unix(), th.ID)
	c.store.db.Exec(`UPDATE thread_log SET at = ? WHERE thread = ?`, now.Add(-5*time.Hour).Unix(), th.ID)

	// Never visited: all of it is new, the thread says it.
	c.keepThreadSurface(th.ID)
	if since, sum := sinceOf(t, c, th.ID); len(since) != 0 || sum != "" {
		t.Fatalf("never visited: %q %q", since, sum)
	}

	// The page opens it, then back later, nothing moved: still nothing.
	first := now.Add(-3 * time.Hour)
	c.threadVisited(th.ID, first)
	second := now.Add(-2 * time.Hour)
	c.threadVisited(th.ID, second)
	c.logThread(th.ID, "engine", "Carte liée : access:7.") // a move code words no fact of
	if since, _ := sinceOf(t, c, th.ID); len(since) != 0 {
		t.Fatalf("nothing new: %q", since)
	}

	// The summary written again: a fact, and the summary in the data.
	c.setThread(th.ID, map[string]any{"summary": "The contact page is written (PR 14)."})
	th, _ = c.thread(th.ID)
	if since, sum := sinceOf(t, c, th.ID); len(since) != 1 || !strings.HasSuffix(since[0], "Résumé mis à jour") || sum != "The contact page is written (PR 14)." {
		t.Fatalf("a fact: %q %q", since, sum)
	}

	// Opened again within threadVisitSame: the same visit, seen stays.
	c.threadVisited(th.ID, second.Add(time.Minute))
	if th, _ = c.thread(th.ID); th.seen != first.Unix() {
		t.Fatalf("seen %d, want %d", th.seen, first.Unix())
	}

	// The page's "open" is a visit; the action stays noted on the surface.
	if err := c.surfaceAction(context.Background(), threadSurfaceID(th.ID), a2ui.Action{Name: a2ui.ActionOpen, SurfaceID: threadSurfaceID(th.ID), SourceComponentID: "root"}); err != nil {
		t.Fatal(err)
	}
	if th, _ = c.thread(th.ID); th.seen != second.Add(time.Minute).Unix() {
		t.Fatalf("after open: seen %d", th.seen)
	}
	s, _ := c.surface(threadSurfaceID(th.ID))
	if s.Look.Action == nil || s.Look.Action.Name != a2ui.ActionOpen {
		t.Errorf("the open was not noted: %+v", s.Look.Action)
	}
	if since, _ := sinceOf(t, c, th.ID); len(since) != 1 || !strings.HasSuffix(since[0], "Résumé mis à jour") {
		t.Errorf("after open, the summary written since: %q", since)
	}
}

// The facts are code's, from the steps' starts and ends after the visit,
// oldest first, the last threadFactsMax; and what waits for the user is
// under « À toi ».
func TestThreadHeaderFactsAndYours(t *testing.T) {
	c := startCore(t)
	now := time.Now()
	at := func(m int) time.Time { return now.Add(time.Duration(m) * time.Minute) }
	s := Thread{ID: 4242, State: threadOpen, Mode: modeLed, Phase: "running", Created: at(-600), seen: at(-120).Unix()}
	steps := []threadSurfaceStep{
		{Label: "Old", State: "done", Started: at(-500), Ended: at(-400)},
		{Label: "Home page", State: "done", Started: at(-200), Ended: at(-100)},
		{Label: "Contact page", State: "current", Started: at(-90)},
		{Label: "Tests", State: "failed", Started: at(-80), Ended: at(-70)},
		{Label: "Review", State: "current", Started: at(-60), Ended: at(-50)},
		{Label: "Deploy", Kind: stepUser, State: "current", Started: at(-40), Waits: true, Yours: true, Key: "deploy"},
	}
	facts := threadFacts(s, steps, s.seen, "fr")
	var texts []string
	for _, f := range facts {
		texts = append(texts, f.Text)
	}
	want := []string{"« Home page » : fait", "« Contact page » a démarré", "« Tests » : en échec", "« Review » a rendu son travail", "« Deploy » t'attend"}
	if strings.Join(texts, "|") != strings.Join(want, "|") {
		t.Fatalf("facts %q", texts)
	}
	if en := threadFacts(s, steps[:2], s.seen, "en"); len(en) != 1 || en[0].Text != "“Home page”: done" {
		t.Errorf("en: %+v", en)
	}

	// More than threadFactsMax: the last ones, and how many came before.
	extra := append(steps, threadSurfaceStep{Label: "Notes", State: "done", Started: at(-30), Ended: at(-20)})
	since := c.threadSinceData(s, extra, now)
	if len(since) != 6 || !strings.HasPrefix(since[0].(string), "… et 1 plus tôt") || !strings.HasSuffix(since[5].(string), "« Notes » : fait") {
		t.Errorf("capped: %v", since)
	}

	yours := func(steps []threadSurfaceStep) (map[string]string, surfaceIn) {
		in := surfaceIn{Weight: threadWeight}
		var out threadComps
		c.threadYours(&in, &out, s, steps, nil, nil)
		texts := map[string]string{}
		for _, id := range out.children {
			texts[id] = ""
		}
		for _, comp := range out.list {
			if text, ok := comp["text"].(string); ok {
				texts[comp["id"].(string)] = text
			}
		}
		return texts, in
	}
	// À toi: the user's step, with its « C'est fait », and the weight.
	if h, in := yours(extra); h["thread_user_5_label"] != "Deploy" || h["thread_yours"] != "À toi" || in.Weight != threadWeightYours {
		t.Errorf("yours: %v weight %d", h, in.Weight)
	}
	// Nothing waits: no « À toi », the usual weight.
	if h, in := yours(steps[:5]); h["thread_yours"] != "" || len(h) != 0 || in.Weight != threadWeight {
		t.Errorf("nothing yours: %v weight %d", h, in.Weight)
	}
	// An ended thread has no « À toi »; its end is a fact.
	s.State, s.Ended = threadDone, at(-10)
	if h, _ := yours(extra); len(h) != 0 {
		t.Errorf("ended: %v", h)
	}
	if since := c.threadSinceData(s, steps[:5], now); !strings.HasSuffix(since[len(since)-1].(string), "Fait ✓") {
		t.Errorf("ended: %v", since)
	}
}

// threadClock words a moment absolutely: the clock the same day, the date
// before it, in the user's language.
func TestThreadClock(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 11, 15, 16, 0, 0, 0, loc)
	for _, tc := range []struct {
		at   time.Time
		lang string
		want string
	}{
		{time.Date(2026, 11, 15, 14, 32, 0, 0, loc), "fr", "14 h 32"},
		{time.Date(2026, 11, 13, 9, 5, 0, 0, loc), "fr", "13 nov. 9 h 05"},
		{time.Date(2026, 11, 15, 14, 32, 0, 0, loc), "en", "14:32"},
		{time.Date(2026, 11, 13, 9, 5, 0, 0, loc), "en", "Nov 13, 09:05"},
	} {
		if got := threadClock(tc.at, now, loc, tc.lang); got != tc.want {
			t.Errorf("%s %s: %q, want %q", tc.at, tc.lang, got, tc.want)
		}
	}
}
