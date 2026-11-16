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
	if _, errs, err := c.surfaceWrite(surfaceAuthorBrain, surfaceIn{ID: "mine", Title: "Mine", Components: json.RawMessage(button)}); err == nil && len(errs) == 0 {
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

	// A light thread runs no steps of its own: no controls, whatever its
	// end (it is dropped, or ends, through Eriphe).
	for _, ends := range []string{endsGoal, endsDay, endsStanding} {
		th, err := c.openThread(threadIn{Title: "T " + ends, Goal: "Shown.", Ends: ends}, nil, byBrain)
		if err != nil {
			t.Fatal(err)
		}
		if s, _, _ := threadSurfaceOf(t, c, th.ID); strings.Contains(string(s.Components), `"thread_controls"`) {
			t.Errorf("controls on a light %s thread: %s", ends, s.Components)
		}
	}
}
