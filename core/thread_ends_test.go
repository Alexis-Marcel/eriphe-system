package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A day thread's end at midnight is no breakpoint: what is held stays
// held; another thread's end, the user there, is one.
func TestDayEndIsNoBreakpoint(t *testing.T) {
	c, _, now := pollCore(t)
	c.attend(attentionItem{Ref: "mail:1", Weight: 3, Why: "a bill", Say: "Ta facture.", Source: "skill:mail", Time: *now})
	ownerSays(c, "Salut")
	brainSays(c, "Salut Sam.")
	c.attentionAnswered(*now)
	c.attend(attentionItem{Ref: "mail:2", Weight: 3, Why: "a parcel", Say: "Ton colis.", Source: "skill:mail", Time: *now})
	c.attentionAnswered(*now)
	if !attentionOf(c, "mail:2").Held {
		t.Fatal("not held")
	}
	c.attentionOnEvent(journalEvent{Kind: evThreadEnded, Payload: map[string]any{"thread": 1, "ends": endsDay}})
	if !attentionOf(c, "mail:2").Held {
		t.Error("a day thread's end told what was held")
	}
	c.attentionOnEvent(journalEvent{Kind: evThreadEnded, Payload: map[string]any{"thread": 2, "ends": endsGoal}})
	if it := attentionOf(c, "mail:2"); it != nil && it.Held {
		t.Error("a thread's end, the user there, told nothing")
	}
}

// Each end kind across a midnight tick (#571): a standing thread stays
// open and shown, and refuses close and stop; a day thread ends at the
// user's midnight and leaves the workspace at once; a goal thread ended
// stays until midnight, its « Ranger » there.
func TestThreadEndsAcrossMidnight(t *testing.T) {
	c := startCore(t)
	standing, err := c.openThread(threadIn{Title: "Mes mails", Goal: "Ce qui arrive.", Alias: "topic:mails", Ends: endsStanding}, nil, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	day, err := c.openThread(threadIn{Title: "Demain", Goal: "Ce que j'ai demain.", Alias: "topic:demain", Ends: endsDay}, nil, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	goal, err := c.openThread(threadIn{Title: "PR 12", Goal: "Merged.", Alias: "pr:example/site#12"}, nil, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	if goal.Ends != endsGoal || standing.Ends != endsStanding || day.Ends != endsDay {
		t.Fatalf("ends %q %q %q", goal.Ends, standing.Ends, day.Ends)
	}
	if err := c.closeLight(standing.ID, "Rien à faire.", byBrain); !errors.Is(err, errStanding) {
		t.Errorf("a standing thread closed: %v", err)
	}
	if err := c.stopThread(t.Context(), standing.ID, "user"); !errors.Is(err, errStanding) {
		t.Errorf("a standing thread stopped: %v", err)
	}
	if err := c.closeLight(goal.ID, "Merged as 4f2c.", byBrain); err != nil {
		t.Fatal(err)
	}

	// « j'ai quoi demain ? »: the day thread's part.
	if _, err := c.writeThreadPart(surfaceCaller{author: surfaceAuthorBrain, taint: func() []string { return nil }}, day.ID,
		surfaceWriteIn{ID: threadSurfaceID(day.ID), Block: "jour", Components: oneText("Dentiste à 10 h")}); err != nil {
		t.Fatal(err)
	}

	// The tick before midnight changes nothing; the one after it ends the
	// day thread, its part with it.
	loc := c.userZone()
	local := time.Now().In(loc)
	midnight := time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, loc)
	c.threadsTick(midnight.Add(-time.Second))
	if th, _ := c.thread(day.ID); !th.active() {
		t.Fatal("the day thread ended before midnight")
	}
	c.threadsTick(midnight.Add(time.Second))
	c.tickSurfaces(t.Context(), midnight.Add(time.Hour+2*time.Second))
	if th, _ := c.thread(day.ID); th.State != threadDone {
		t.Fatalf("the day thread is %s after midnight", th.State)
	}
	if _, err := c.surface(threadSurfaceID(day.ID)); err == nil {
		t.Error("the day thread is still shown after its day")
	}
	if c.threadPartOf(day.ID) != nil {
		t.Error("the day's part outlived its day")
	}
	if th, _ := c.thread(standing.ID); !th.active() {
		t.Error("the standing thread ended")
	}
	if _, err := c.surface(threadSurfaceID(standing.ID)); err != nil {
		t.Error("the standing thread is no longer shown")
	}
	if _, err := c.surface(threadSurfaceID(goal.ID)); err == nil {
		t.Error("the ended goal thread is still shown the next day")
	}
	if th, _ := c.thread(goal.ID); th.State != threadDone {
		t.Errorf("the goal thread is %s", th.State)
	}

	// Reopened, a day thread's day is the reopening's: one opened
	// yesterday, ended by the tick, then reopened, is kept by the next.
	c.store.db.Exec(`UPDATE threads SET started = ? WHERE id = ?`, time.Now().Add(-48*time.Hour).Unix(), day.ID)
	if _, err := c.reopenThread(day.ID, "Encore une question.", byBrain); err != nil {
		t.Fatal(err)
	}
	c.threadsTick(time.Now())
	if th, _ := c.thread(day.ID); !th.active() {
		t.Error("a reopened day thread ended at once")
	}
	if s, err := c.surface(threadSurfaceID(day.ID)); err != nil || strings.Contains(string(s.Components), "Dentiste") {
		t.Errorf("reopened, the day thread shows yesterday's part, or nothing: %v", err)
	}
	// Asked again the next day, the same thread, open.
	if again, _, err := c.openThreadOnce(threadIn{Title: "Demain", Goal: "Ce que j'ai demain.", Alias: "topic:demain", Ends: endsDay}, nil, byBrain); err != nil || again.ID != day.ID || !again.active() {
		t.Errorf("asked again: %+v %v", again, err)
	}

	// Asked to stand, the thread its subject has becomes a standing one.
	got, _, err := c.openThreadOnce(threadIn{Title: "PR 12", Goal: "Merged.", Alias: "pr:example/site#12", Ends: endsStanding}, nil, byBrain)
	if err != nil || got.ID != goal.ID || got.Ends != endsStanding || !got.active() {
		t.Fatalf("again: %+v %v", got, err)
	}
	// Ends is one of three.
	if _, err := c.openThread(threadIn{Title: "x", Goal: "y", Alias: "topic:x", Ends: "forever"}, nil, byBrain); err == nil {
		t.Error("ends forever accepted")
	}
}

// « Ranger » on an ended goal thread's surface: the Button code placed
// there puts it away at once; the thread is kept, and found again.
// Nothing else sends it: not an author, not another thread's id.
func TestThreadTidy(t *testing.T) {
	c := startCore(t)
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	th, err := c.openThread(threadIn{Title: "PR 12", Goal: "Merged.", Alias: "pr:example/site#12"}, nil, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	s, _, _ := threadSurfaceOf(t, c, th.ID)
	if strings.Contains(string(s.Components), threadTidyID) {
		t.Fatal("« Ranger » on an open thread")
	}
	tidy := func(ctx string) (int, string) {
		return postJSON(tw, "/api/espace/action", actionOn("tidy", s.ID, threadTidyID, ctx), csrf)
	}
	right := fmt.Sprintf(`{"thread":%d}`, th.ID)
	if code, b := tidy(right); code != http.StatusBadRequest {
		t.Errorf("tidy with no button: %d %s", code, b)
	}
	if err := c.closeLight(th.ID, "Merged as 4f2c.", byBrain); err != nil {
		t.Fatal(err)
	}
	s, _, _ = threadSurfaceOf(t, c, th.ID)
	if !strings.Contains(string(s.Components), `"name":"tidy"`) || !strings.Contains(string(s.Components), "Ranger") {
		t.Fatalf("no « Ranger »: %s", s.Components)
	}
	if code, b := tidy(fmt.Sprintf(`{"thread":%d}`, th.ID+1)); code != http.StatusBadRequest {
		t.Errorf("another thread: %d %s", code, b)
	}
	if code, b := tidy(right); code != http.StatusNoContent {
		t.Fatalf("tidy: %d %s", code, b)
	}
	if _, err := c.surface(s.ID); err == nil {
		t.Error("still shown once put away")
	}
	c.keepThreadSurface(th.ID)
	if _, err := c.surface(s.ID); err == nil {
		t.Error("shown again once put away")
	}
	if found, _ := c.findThreads("pr:example/site#12", "", true, 5); len(found) != 1 || found[0].ID != th.ID {
		t.Errorf("not found again: %+v", found)
	}
	if err := c.tidyThread(th.ID, "user"); !errors.Is(err, errNotTidy) {
		t.Errorf("put away twice: %v", err)
	}
	// Reopened, it is shown again.
	if _, err := c.reopenThread(th.ID, "The deploy failed.", byBrain); err != nil {
		t.Fatal(err)
	}
	if _, err := c.surface(s.ID); err != nil {
		t.Error("reopened, not shown")
	}
	button := fmt.Sprintf(`[{"id":"root","component":"Button","label":"Ranger","action":{"event":{"name":"tidy","context":{"thread":%d}}}}]`, th.ID)
	if _, errs, err := c.fixtureSurface(surfaceAuthorBrain, surfaceIn{ID: "mine", Title: "Mine", Components: json.RawMessage(button)}); err == nil && len(errs) == 0 {
		t.Error("an author wrote tidy")
	}
}

// A threads table made before ends and tidied gets both at open: its
// threads end on their goal.
func TestOpenStoreAddsThreadEnds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "core.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE threads (id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT NOT NULL, goal TEXT NOT NULL,
	done_when TEXT NOT NULL DEFAULT '', mode TEXT NOT NULL DEFAULT 'light', about TEXT NOT NULL DEFAULT '', state TEXT NOT NULL,
	phase TEXT NOT NULL DEFAULT '', held TEXT NOT NULL DEFAULT '', budget INTEGER NOT NULL DEFAULT 0, amends INTEGER NOT NULL DEFAULT 0,
	summary TEXT NOT NULL DEFAULT '', tainted TEXT NOT NULL DEFAULT '', lead TEXT NOT NULL DEFAULT '', told TEXT NOT NULL DEFAULT '',
	spent INTEGER NOT NULL DEFAULT 0, since INTEGER NOT NULL DEFAULT 0, created INTEGER NOT NULL, started INTEGER NOT NULL DEFAULT 0,
	ended INTEGER NOT NULL DEFAULT 0, summarized INTEGER NOT NULL DEFAULT 0, seen INTEGER NOT NULL DEFAULT 0, visited INTEGER NOT NULL DEFAULT 0);
INSERT INTO threads (title, goal, state, created) VALUES ('x', 'y', 'open', 1);`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	c := &core{store: s}
	th, err := c.thread(1)
	if err != nil || th.Ends != endsGoal || th.tidied != 0 {
		t.Fatalf("%+v %v", th, err)
	}
}
