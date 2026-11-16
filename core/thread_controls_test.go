package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A choice that waits is answered on the thread's surface: one button per
// option, each bound by code to its thread, its step and its option; the
// page cannot change the option, nor an author write one.
func TestThreadSurfaceChoice(t *testing.T) {
	c := startCore(t)
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	th, err := c.openThread(threadIn{Title: "Shop", Goal: "A name for the shop."}, nil, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	c.insertSteps(th.ID, []stepSpec{{Key: "pick", Title: "Le nom", Kind: stepChoice, Question: "Lequel ?", Options: []string{"Lumen", "Aster"}}}, false)
	c.setStep(th.ID, "pick", map[string]any{"status": stepRunning, "started": time.Now().Unix()})
	s, _, meta := threadSurfaceOf(t, c, th.ID)
	if !strings.Contains(string(s.Components), `"thread_choice_0_1"`) || !strings.Contains(string(s.Components), `"name":"choose"`) || meta.State != tileYours {
		t.Fatalf("no choice: %s (%s)", s.Components, meta.State)
	}
	choose := func(comp, ctx string) (int, string) {
		return postJSON(tw, "/api/espace/action", actionOn("choose", s.ID, comp, ctx), csrf)
	}
	aster := fmt.Sprintf(`{"thread":%d,"step":"pick","option":"Aster"}`, th.ID)
	if code, b := choose("thread_choice_0_0", aster); code != http.StatusBadRequest {
		t.Errorf("another option than its button's: %d %s", code, b)
	}
	if code, b := choose("thread_choice_0_label", aster); code != http.StatusBadRequest {
		t.Errorf("not a button: %d %s", code, b)
	}
	if code, b := choose("thread_choice_0_1", aster); code != http.StatusNoContent {
		t.Fatalf("choose: %d %s", code, b)
	}
	if st, _ := c.step(th.ID, "pick"); st.Status != stepAccepted || st.Output != "Aster" {
		t.Fatalf("not chosen: %+v", st)
	}
	if code, _ := choose("thread_choice_0_1", aster); code != http.StatusBadRequest && code != http.StatusNotFound {
		t.Errorf("chosen twice: %d", code)
	}
	button := fmt.Sprintf(`[{"id":"root","component":"Button","label":"Aster","action":{"event":{"name":"choose","context":%s}}}]`, aster)
	if _, errs, err := c.fixtureSurface(surfaceAuthorBrain, surfaceIn{ID: "mine", Title: "Mine", Components: json.RawMessage(button)}); err == nil && len(errs) == 0 {
		t.Error("an author wrote choose")
	}
}

// A thread that runs steps is paused, resumed and stopped from its
// surface, by the buttons code wrote there; a button's context cannot be
// changed by the page.
func TestThreadSurfaceControls(t *testing.T) {
	c, th := threadCore(t)
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	c.decideCard(context.Background(), threadCard(t, c, askPlan).ID, true, "web")
	sid := threadSurfaceID(th.ID)
	control := func(comp, do string) (int, string) {
		return postJSON(tw, "/api/espace/action", actionOn("control", sid, comp, fmt.Sprintf(`{"thread":%d,"do":%q}`, th.ID, do)), csrf)
	}
	s, _, _ := threadSurfaceOf(t, c, th.ID)
	if !strings.Contains(string(s.Components), `"thread_control_pause"`) || !strings.Contains(string(s.Components), `"thread_control_stop"`) {
		t.Fatalf("no controls: %s", s.Components)
	}
	if code, b := control("thread_control_pause", "stop"); code != http.StatusBadRequest {
		t.Errorf("pause's button sent stop: %d %s", code, b)
	}
	if code, b := control("thread_control_pause", "pause"); code != http.StatusNoContent {
		t.Fatalf("pause: %d %s", code, b)
	}
	if th, _ = c.thread(th.ID); th.Phase != phasePaused {
		t.Fatalf("not paused: %s", th.Phase)
	}
	if s, _, _ = threadSurfaceOf(t, c, th.ID); !strings.Contains(string(s.Components), `"thread_control_resume"`) {
		t.Fatalf("no resume: %s", s.Components)
	}
	if code, b := control("thread_control_resume", "resume"); code != http.StatusNoContent {
		t.Fatalf("resume: %d %s", code, b)
	}
	if code, b := control("thread_control_stop", "stop"); code != http.StatusNoContent {
		t.Fatalf("stop: %d %s", code, b)
	}
	if th, _ = c.thread(th.ID); th.active() {
		t.Fatalf("still active: %s", th.Phase)
	}
	if s, _, _ = threadSurfaceOf(t, c, th.ID); strings.Contains(string(s.Components), `"thread_controls"`) {
		t.Errorf("controls on an ended thread: %s", s.Components)
	}

	// A light thread runs no steps of its own: Stop only, by the same
	// button, the user's (it is dropped). A standing one never ends: none.
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
		if !strings.Contains(comps, `"thread_control_stop"`) || strings.Contains(comps, `"thread_control_pause"`) || strings.Contains(comps, `"thread_control_resume"`) {
			t.Fatalf("a light %s thread's controls: %s", ends, comps)
		}
		on := func(do string) (int, string) {
			return postJSON(tw, "/api/espace/action", actionOn("control", sid, "thread_control_stop", fmt.Sprintf(`{"thread":%d,"do":%q}`, th.ID, do)), csrf)
		}
		if code, b := on("pause"); code != http.StatusBadRequest {
			t.Errorf("stop's button sent pause: %d %s", code, b)
		}
		if code, b := on("stop"); code != http.StatusNoContent {
			t.Fatalf("stop a light %s thread: %d %s", ends, code, b)
		}
		if th, _ = c.thread(th.ID); th.active() || th.State != threadDropped {
			t.Fatalf("a light %s thread not dropped: %s", ends, th.State)
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
