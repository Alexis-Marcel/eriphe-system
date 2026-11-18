package main

import (
	"context"
	"strings"
	"testing"
)

// A thread's goal is the brain's and the agents' target, written for them:
// the user never reads it, not on its surface, its chip, its ended banner
// nor its plan card.
func TestThreadGoalShownToNoOne(t *testing.T) {
	c := startCore(t)
	const goal = "Montrer à Sam ses projets en cours dans son coffre"
	out, isErr := callBrainTool(t, c, "open_thread", map[string]any{"title": "Projets en cours", "goal": goal + ".", "alias": "topic:projets"})
	if isErr {
		t.Fatalf("open_thread: %v", out)
	}
	ss, _ := c.threads(true, 10)
	if len(ss) != 1 {
		t.Fatalf("threads %+v", ss)
	}
	id := ss[0].ID
	shows := func(when string) {
		t.Helper()
		s, _, meta := threadSurfaceOf(t, c, id)
		if strings.Contains(string(s.Components), goal) || strings.Contains(string(s.Data), goal) ||
			strings.Contains(meta.Summary, goal) || strings.Contains(s.Title, goal) {
			t.Fatalf("%s: the goal is shown: %s %s %+v", when, s.Components, s.Data, meta)
		}
	}
	shows("open")
	if err := c.stopThread(context.Background(), id, byBrain); err != nil {
		t.Fatal(err)
	}
	shows("ended")
}

// The brain gives the thread it opens a headline: the line the user reads
// at its top and on its chip, checked before anything opens; opened again
// on its alias with another, it is replaced, and without one it stays.
func TestOpenThreadHeadline(t *testing.T) {
	c := startCore(t)
	open := map[string]any{"title": "Projets en cours", "goal": "Montrer à Sam ses projets.", "alias": "topic:projets",
		"headline": strings.Repeat("x", threadHeadlineMax+1)}
	if out, isErr := callBrainTool(t, c, "open_thread", open); !isErr {
		t.Fatalf("a long headline: %v", out)
	}
	if ss, _ := c.threads(true, 10); len(ss) != 0 {
		t.Fatalf("opened despite its headline: %+v", ss)
	}

	open["headline"] = "Tes 4 projets en cours, avec leur prochaine étape"
	if out, isErr := callBrainTool(t, c, "open_thread", open); isErr {
		t.Fatalf("open_thread: %v", out)
	}
	ss, _ := c.threads(true, 10)
	if len(ss) != 1 {
		t.Fatalf("threads %+v", ss)
	}
	id := ss[0].ID
	_, data, meta := threadSurfaceOf(t, c, id)
	if state, _ := data["state"].(string); !strings.HasPrefix(state, "Tes 4 projets en cours") || meta.Summary != "Tes 4 projets en cours, avec leur prochaine étape" {
		t.Fatalf("headline: %q %+v", state, meta)
	}
	if h := c.threadHeadlineOf(id); h == nil || h.By != byBrain || len(h.Tainted) != 0 {
		t.Fatalf("stored %+v", h)
	}

	delete(open, "headline")
	if out, isErr := callBrainTool(t, c, "open_thread", open); isErr || !strings.Contains(out["note"].(string), "Not a new thread") {
		t.Fatalf("again: %v", out)
	}
	if h := c.threadHeadlineOf(id); h == nil || h.Text != "Tes 4 projets en cours, avec leur prochaine étape" {
		t.Fatalf("no headline took it away: %+v", h)
	}
	open["headline"] = "Tes projets : un en retard"
	callBrainTool(t, c, "open_thread", open)
	if _, _, meta := threadSurfaceOf(t, c, id); meta.Summary != "Tes projets : un en retard" {
		t.Fatalf("replaced: %+v", meta)
	}
}
