package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A thread is stopped from its surface, by the button code wrote there;
// a button's context cannot be changed by the page. A standing one never
// ends: no button.
func TestThreadSurfaceControls(t *testing.T) {
	c := startCore(t)
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	for _, ends := range []string{endsGoal, endsDay, endsStanding} {
		th, err := c.openThread(threadIn{Title: "T " + ends, Goal: "Shown.", Ends: ends}, nil, byBrain)
		if err != nil {
			t.Fatal(err)
		}
		sid := threadSurfaceID(th.ID)
		s, _, _ := threadSurfaceOf(t, c, th.ID)
		comps := string(s.Components)
		if ends == endsStanding {
			if strings.Contains(comps, `"thread_controls"`) {
				t.Errorf("controls on a standing thread: %s", comps)
			}
			continue
		}
		if !strings.Contains(comps, `"thread_control_stop"`) || strings.Contains(comps, `"thread_control_pause"`) {
			t.Fatalf("a %s thread's controls: %s", ends, comps)
		}
		on := func(do string) (int, string) {
			return postJSON(tw, "/api/espace/action", actionOn("control", sid, "thread_control_stop", fmt.Sprintf(`{"thread":%d,"do":%q}`, th.ID, do)), csrf)
		}
		if code, b := on("pause"); code != http.StatusBadRequest {
			t.Errorf("stop's button sent pause: %d %s", code, b)
		}
		if code, b := on("stop"); code != http.StatusNoContent {
			t.Fatalf("stop a %s thread: %d %s", ends, code, b)
		}
		if th, _ = c.thread(th.ID); th.active() || th.State != threadDropped {
			t.Fatalf("a %s thread not dropped: %s", ends, th.State)
		}
	}
}

// A link to an ended thread whose surface has left the workspace shows it
// again, as long as one that just ended, for the page to open; not one the
// user put away, nor a day's, nor through another id.
func TestLinkShowsEndedThread(t *testing.T) {
	c := startCore(t)
	tw := newTestWebFor(t, c)
	tw.setup()
	ended := func(title, ends string) Thread {
		t.Helper()
		th, err := c.openThread(threadIn{Title: title, Goal: "Done.", Ends: ends}, nil, byBrain)
		if err != nil {
			t.Fatal(err)
		}
		if err := c.stopThread(context.Background(), th.ID, byBrain); err != nil {
			t.Fatal(err)
		}
		c.surfaceRemove(threadSurfaceID(th.ID), surfaceByLifetime) // its time on the workspace is over
		th, _ = c.thread(th.ID)
		return th
	}
	shown := func(id int64) bool {
		_, err := c.surface(threadSurfaceID(id))
		return err == nil
	}
	open := func(ref string) {
		t.Helper()
		if code, _, _ := tw.do("GET", "/espace?open="+ref, nil, nil); code != http.StatusOK {
			t.Fatalf("GET /espace?open=%s: %d", ref, code)
		}
	}

	goal := ended("Le garage", endsGoal)
	c.keepThreadSurface(goal.ID)
	if shown(goal.ID) {
		t.Fatal("an ended thread came back by itself")
	}
	open("mine")
	open(threadSurfaceID(goal.ID))
	s, _, _ := threadSurfaceOf(t, c, goal.ID)
	if !s.Lifetime.Until.After(time.Now().Add(30*time.Minute)) || !strings.Contains(string(s.Components), `"thread_tidy"`) {
		t.Fatalf("shown again: until %v, %s", s.Lifetime.Until, s.Components)
	}

	tidied := ended("Le dentiste", endsGoal)
	if err := c.tidyThread(tidied.ID, "user"); err != nil {
		t.Fatal(err)
	}
	day := ended("Ma journée", endsDay)
	open(threadSurfaceID(tidied.ID))
	open(threadSurfaceID(day.ID))
	if shown(tidied.ID) || shown(day.ID) {
		t.Error("a thread put away, or a day's, came back")
	}
}
