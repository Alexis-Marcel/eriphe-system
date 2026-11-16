package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
)

// headerOf is the header's texts of a thread's surface, by component id:
// literal ones, and the facts bound under /thread/facts.
func headerOf(t *testing.T, c *core, id int64) map[string]string {
	t.Helper()
	s, data, _ := threadSurfaceOf(t, c, id)
	var list []map[string]any
	json.Unmarshal(s.Components, &list)
	out := map[string]string{}
	facts, _ := data["facts"].([]any)
	for _, comp := range list {
		cid, _ := comp["id"].(string)
		switch text := comp["text"].(type) {
		case string:
			out[cid] = text
		case map[string]any:
			if p, _ := text["@path"].(string); strings.HasPrefix(p, "/thread/facts/") {
				n := 0
				for _, r := range strings.TrimPrefix(p, "/thread/facts/") {
					n = n*10 + int(r-'0')
				}
				out[cid], _ = facts[n].(string)
			}
		}
	}
	if sum, ok := data["summary"].(string); ok {
		out["thread_summary"] = sum
	}
	return out
}

// Opening a thread's surface moves its visits: the header tells what
// changed since the visit before, so the one that opens it still sees
// what was new; never visited, or back with nothing new, it says nothing
// of it, nor « Pas encore de résumé ». Opened again within
// threadVisitSame, the same visit.
func TestThreadHeaderSinceVisit(t *testing.T) {
	c := startCore(t)
	th, err := c.openThread(threadIn{Title: "Site", Goal: "The contact page is online.", Alias: "project:site"}, nil, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	c.store.db.Exec(`UPDATE threads SET created = ? WHERE id = ?`, now.Add(-5*time.Hour).Unix(), th.ID)
	c.store.db.Exec(`UPDATE thread_log SET at = ? WHERE thread = ?`, now.Add(-5*time.Hour).Unix(), th.ID)
	clock := func(at time.Time) string { return threadClock(at, now, c.userZone(), "fr") }

	// Never visited: all of it is new, the thread says it; no history, no
	// « Pas encore de résumé ».
	c.keepThreadSurface(th.ID)
	h := headerOf(t, c, th.ID)
	if h["thread_since"] != "" || h["thread_fact_0"] != "" || h["thread_where"] != "" {
		t.Fatalf("never visited: %v", h)
	}

	// The page opens it, then back later, nothing moved: still nothing.
	first := now.Add(-3 * time.Hour)
	c.threadVisited(th.ID, first)
	second := now.Add(-2 * time.Hour)
	c.threadVisited(th.ID, second)
	c.logThread(th.ID, "engine", "Carte liée : access:7.") // a move code words no fact of
	if h = headerOf(t, c, th.ID); h["thread_since"] != "" || h["thread_fact_0"] != "" {
		t.Fatalf("nothing new: %v", h)
	}

	// The summary written again: a fact, and where it stands, dated.
	c.setThread(th.ID, map[string]any{"summary": "The contact page is written (PR 14)."})
	th, _ = c.thread(th.ID)
	h = headerOf(t, c, th.ID)
	if h["thread_since"] != "Depuis ta dernière visite, "+clock(first) || !strings.HasSuffix(h["thread_fact_0"], "Résumé mis à jour") ||
		h["thread_summary"] != "The contact page is written (PR 14)." || h["thread_summary_at"] != "Résumé mis à jour : "+clock(time.Unix(th.summarized, 0)) {
		t.Fatalf("a fact: %v", h)
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
	if h = headerOf(t, c, th.ID); h["thread_since"] != "Depuis ta dernière visite, "+clock(second.Add(time.Minute)) {
		t.Errorf("after open: %v", h)
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
	header := func(steps []threadSurfaceStep) (map[string]string, surfaceIn) {
		in := surfaceIn{Weight: threadWeight}
		var out threadComps
		data := map[string]any{}
		c.threadSince(&out, data, s, steps, nil, now)
		c.threadWhere(&out, data, s, now)
		c.threadYours(&in, &out, s, steps, nil, nil)
		texts := map[string]string{}
		facts, _ := data["facts"].([]any)
		for _, comp := range out.list {
			id, _ := comp["id"].(string)
			if text, ok := comp["text"].(string); ok {
				texts[id] = text
			} else if strings.HasPrefix(id, "thread_fact_") {
				n := int(id[len(id)-1] - '0')
				texts[id], _ = facts[n].(string)
			}
		}
		for _, id := range out.children {
			if _, ok := texts[id]; !ok {
				texts[id] = ""
			}
		}
		return texts, in
	}
	extra := append(steps, threadSurfaceStep{Label: "Notes", State: "done", Started: at(-30), Ended: at(-20)})
	h, in := header(extra)
	if !strings.HasPrefix(h["thread_fact_0"], "… et 1 plus tôt") || !strings.HasSuffix(h["thread_fact_5"], "« Notes » : fait") || h["thread_fact_6"] != "" {
		t.Errorf("capped: %v", h)
	}
	// À toi: the user's step, with its « C'est fait », and the weight.
	if _, ok := h["thread_user_5"]; !ok || h["thread_yours"] != "À toi" || in.Weight != threadWeightYours {
		t.Errorf("yours: %v weight %d", h, in.Weight)
	}

	// Nothing waits: no « À toi », the usual weight.
	h, in = header(steps[:5])
	if _, ok := h["thread_yours"]; ok || in.Weight != threadWeight {
		t.Errorf("nothing yours: %v weight %d", h, in.Weight)
	}
	// An ended thread has no « À toi ».
	s.State, s.Ended = threadDone, at(-10)
	h, _ = header(steps[:5])
	if _, ok := h["thread_yours"]; ok || !strings.HasSuffix(h["thread_fact_4"], "Fait ✓") {
		t.Errorf("ended: %v", h)
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
