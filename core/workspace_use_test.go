package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The measure from a gesture log: a surface put in front is opened (the
// time it took counts), ignored (folded unopened, or an ignored or closed
// gesture came first), gone (removed unopened) or still in front; one
// opened from the fold is not counted.
func TestSurfaceUseOf(t *testing.T) {
	t0 := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	at := func(d time.Duration) time.Time { return t0.Add(d) }
	g := func(what, s string, d time.Duration) gesture {
		return gesture{What: what, Ref: "surface:" + s, Time: at(d)}
	}
	spans := []frontSpan{
		{Surface: "a", Shown: t0},                                       // opened after 3 min
		{Surface: "b", Shown: t0, Gone: at(10 * time.Minute)},           // folded unopened
		{Surface: "c", Shown: t0},                                       // ignored on the page
		{Surface: "d", Shown: t0},                                       // closed, then opened
		{Surface: "e", Shown: at(30 * time.Second)},                     // opened from the fold
		{Surface: "f", Shown: t0},                                       // still in front
		{Surface: "g", Shown: t0},                                       // opened after 9 min
		{Surface: "h", Shown: t0, Gone: at(time.Minute)},                // opened once folded
		{Surface: "i", Shown: t0, Gone: at(time.Minute), Removed: true}, // settled before it was opened
	}
	gs := []gesture{
		g(gestureOpened, "a", 3*time.Minute),
		g(gestureIgnored, "c", time.Minute),
		g(gestureClosed, "d", 2*time.Minute), g(gestureOpened, "d", 5*time.Minute),
		g(gestureOpened, "e", 29*time.Second),
		g(gestureOpened, "g", 9*time.Minute),
		g(gestureOpened, "h", 5*time.Minute),
		g(gestureOpened, "zz", time.Minute), // never in front
	}
	u := surfaceUseOf(spans, gs)
	want := surfaceUse{Shown: 8, Opened: 2, Ignored: 4, Gone: 1, Front: 1, ToOpen: 6 * time.Minute}
	if u != want {
		t.Fatalf("use %+v, want %+v", u, want)
	}
	if s := u.IgnoredShare(); s < 0.66 || s > 0.67 {
		t.Fatalf("share %v", s)
	}
	if (surfaceUse{}).IgnoredShare() != 0 || surfaceUseOf(nil, gs) != (surfaceUse{}) {
		t.Fatal("nothing in front measured")
	}
	if u := surfaceUseOf(spans[:1], gs); u.ToOpen != 3*time.Minute {
		t.Fatalf("one open: %v", u.ToOpen)
	}
}

// Code keeps what it puts in front as the layout changes, and Settings →
// Surfaces says how it fared, in the user's language.
func TestSurfaceUseFromLayout(t *testing.T) {
	c, _ := surfaceCore(t)
	comps := json.RawMessage(`[{"id":"root","component":"Text","text":"x"}]`)
	a, _, err := c.surfaceWrite("brain", surfaceIn{Title: "a", Components: comps})
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := c.surfaceWrite("brain", surfaceIn{Title: "b", Components: comps})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if u := c.surfaceUseSince(now.Add(-time.Hour), now); u.Shown != 2 || u.Front != 2 {
		t.Fatalf("in front: %+v", u)
	}
	c.relayout()                                                     // nothing changed: nothing starts twice
	c.store.db.Exec(`UPDATE workspace_front SET shown = shown - 60`) // put in front a minute ago
	c.noteGesture(gestureOpened, "surface:"+a.ID, now)
	if err := c.surfaceRemove(b.ID, surfaceByUser); err != nil {
		t.Fatal(err)
	}
	u := c.surfaceUseSince(now.Add(-time.Hour), time.Now())
	if u.Shown != 2 || u.Opened != 1 || u.Gone != 1 || u.Ignored != 0 || u.ToOpen < 59*time.Second {
		t.Fatalf("use %+v", u)
	}

	tw := newTestWebFor(t, c)
	tw.setup()
	code, body, _ := tw.do("GET", "/settings/surfaces", nil, nil)
	if code != 200 || !strings.Contains(body, "2 mises devant, 1 ouvertes, 0 repliées sans être ouvertes (0 %), 1 retirées avant") ||
		!strings.Contains(body, "en médiane") || !strings.Contains(body, `id="surface-`+a.ID+`"`) {
		t.Fatalf("page %d:\n%s", code, body)
	}
	if got := useWords(u, "en"); !strings.Contains(got, "2 put in front, 1 opened, 0 folded unopened (0%), 1 removed first") || !strings.Contains(got, "median") {
		t.Fatalf("en: %s", got)
	}
	if got := useWords(surfaceUse{}, "en"); got != tr("en", "surfaces.use.none") {
		t.Fatalf("none: %s", got)
	}
	for d, want := range map[time.Duration]string{40 * time.Second: "40 s", 12 * time.Minute: "12 min", 50 * time.Hour: "2 d 2 h"} {
		if got := useSpan(d, "en"); got != want {
			t.Errorf("useSpan(%s) = %q, want %q", d, got, want)
		}
	}
}
