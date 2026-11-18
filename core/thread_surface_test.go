package main

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

// threadSurfaceOf is a thread's surface, code's data in it (/thread) and
// what the page reads around it.
func threadSurfaceOf(t *testing.T, c *core, id int64) (surface, map[string]any, surfaceMeta) {
	t.Helper()
	s, err := c.surface(threadSurfaceID(id))
	if err != nil {
		t.Fatalf("thread %d has no surface: %v", id, err)
	}
	var data map[string]any
	json.Unmarshal(s.Data, &data)
	th, _ := data[threadDataKey].(map[string]any)
	var meta struct {
		Extensions map[string]surfaceMeta `json:"extensions"`
	}
	json.Unmarshal(c.createMessage(s).CreateSurface.Metadata, &meta)
	return s, th, meta.Extensions[surfaceExtension]
}

// itemSources are the sources a thread's data gives (/thread/items): their
// names, sorted, and each one's data by name.
func itemSources(data map[string]any) ([]string, map[string]map[string]any) {
	all, _ := data["items"].(map[string]any)
	by := map[string]map[string]any{}
	for name, v := range all {
		by[name], _ = v.(map[string]any)
	}
	return slices.Sorted(maps.Keys(by)), by
}

// sourceRows are the rows a source of a thread's surface shows, its
// groups' in order.
func sourceRows(src map[string]any) []any {
	var rows []any
	groups, _ := src["groups"].([]any)
	for _, g := range groups {
		items, _ := g.(map[string]any)["items"].([]any)
		rows = append(rows, items...)
	}
	return rows
}

// stepItems are a thread's steps as its data gives them (/thread/steps):
// those done, then the others, each a Timeline item.
func stepItems(data map[string]any) []any {
	steps, _ := data["steps"].(map[string]any)
	done, _ := steps["done"].([]any)
	open, _ := steps["open"].([]any)
	return append(append([]any{}, done...), open...)
}

// timelineOf is the Timeline items a thread's data gives: its steps, and
// its monitors' events (/thread/items/<monitor>/events): label → state,
// and each one's detail.
func timelineOf(data map[string]any) (map[string]string, map[string]string) {
	states, details := map[string]string{}, map[string]string{}
	items := stepItems(data)
	sources, _ := data["items"].(map[string]any)
	for _, src := range sources {
		events, _ := src.(map[string]any)["events"].([]any)
		items = append(items, events...)
	}
	for _, it := range items {
		m, _ := it.(map[string]any)
		label, _ := m["label"].(string)
		states[label], _ = m["state"].(string)
		details[label], _ = m["detail"].(string)
	}
	return states, details
}

// surfSeq is the number of the last change sent to the page.
func surfSeq(c *core) int64 {
	c.surf.mu.Lock()
	defer c.surf.mu.Unlock()
	return c.surf.seq
}

// A light thread has its surface from its opening, code's: the brain
// neither rewrites nor removes it. Done, it stays the rest of its day (an
// hour at least), then leaves and does not come back; reopened, it has it
// again.
func TestThreadSurfaceLight(t *testing.T) {
	c := startCore(t)
	th, err := c.openThread(threadIn{Title: "PR 12", Goal: "Merged and deployed.", Alias: "pr:example/site#12"}, nil, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	sid, ref := threadSurfaceID(th.ID), fmt.Sprintf("thread:%d", th.ID)
	s, data, meta := threadSurfaceOf(t, c, th.ID)
	if s.Author != ref || s.Lifetime.While != ref || s.Title != "PR 12" || strings.Contains(string(s.Data), "Merged and deployed") {
		t.Fatalf("surface %+v data %v", s, data)
	}
	if meta.Summary != "Ouvert" || meta.Why != "" || meta.Tainted || !meta.Answers {
		t.Errorf("meta %+v", meta)
	}
	// Open and held by the brain: nothing yet, no state line, no empty
	// timeline, no « À toi » with nothing in it.
	if strings.Contains(string(s.Components), "thread_steps") || data["state"] != nil || strings.Contains(string(s.Components), "thread_yours") ||
		!strings.Contains(string(s.Components), "Rien pour l'instant.") {
		t.Errorf("components %s data %v", s.Components, data)
	}

	// Code's: the brain writes a part, never the whole, and removes nothing.
	whole := surfaceIn{ID: sid, Title: "Mine", Components: json.RawMessage(`[{"id":"root","component":"Text","text":"x"}]`)}
	if _, _, err := c.fixtureSurface(surfaceAuthorBrain, whole); err == nil {
		t.Error("the brain rewrote a thread's surface")
	}
	if err := c.surfaceRemove(sid, surfaceAuthorBrain); err == nil {
		t.Error("the brain removed a thread's surface")
	}
	if _, _, err := c.writeSurface("skill:revue", surfaceIn{ID: threadSurfaceID(th.ID + 1), Title: "x", Components: whole.Components, Weight: 50, Lifetime: surfaceLifetime{Kept: true}}); err == nil {
		t.Error("a skill took the id of a thread's surface")
	}

	// Nothing changed that it shows: nothing is sent.
	seq := surfSeq(c)
	c.setThread(th.ID, map[string]any{"told": "complete"})
	if surfSeq(c) != seq {
		t.Error("written again with nothing new")
	}

	if err := c.closeLight(th.ID, "Merged as 4f2c, deployed at 10:00.", byBrain); err != nil {
		t.Fatal(err)
	}
	th, _ = c.thread(th.ID)
	s, data, meta = threadSurfaceOf(t, c, th.ID)
	until := s.Lifetime.Until.In(c.userZone())
	midnight := until.Hour() == 0 && until.Minute() == 0 && until.Second() == 0
	if s.Lifetime.While != "" || until.Sub(th.Ended) < threadAfterEnd || (!midnight && until.Sub(th.Ended) != threadAfterEnd) {
		t.Fatalf("lifetime %+v, ended %s", s.Lifetime, th.Ended)
	}
	if ended, _ := data["ended"].(map[string]any); meta.Summary != "Fait ✓" || ended["text"] != "Merged as 4f2c, deployed at 10:00." || s.Weight != threadWeightEnded {
		t.Errorf("done: meta %+v data %v weight %d", meta, data, s.Weight)
	}
	c.tickSurfaces(context.Background(), until.Add(-time.Minute))
	if _, err := c.surface(sid); err != nil {
		t.Fatal("left before its time")
	}
	c.tickSurfaces(context.Background(), until.Add(time.Second))
	if _, err := c.surface(sid); err == nil {
		t.Fatal("still there after its time")
	}
	c.keepThreadSurface(th.ID)
	if _, err := c.surface(sid); err == nil {
		t.Fatal("an ended thread's surface came back")
	}

	if _, err := c.reopenThread(th.ID, "The deploy failed.", byBrain); err != nil {
		t.Fatal(err)
	}
	if s, _, _ = threadSurfaceOf(t, c, th.ID); s.Lifetime.While != ref {
		t.Fatalf("reopened: %+v", s.Lifetime)
	}
}

// A thread drawn from external content answers its own cards in place all
// the same: code placed them; they leave the queue for it.
func TestThreadSurfaceTainted(t *testing.T) {
	c := startCore(t)
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	th, err := c.openThread(threadIn{Title: "Invoice", Goal: "Pay the invoice the mail asks."}, []string{"mail:7"}, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	r := proposedRule(t, c, "facture", "")
	ref := proposalRef(cardRule, r.ID)
	if _, err := c.linkThread(th.ID, ref, byBrain); err != nil {
		t.Fatal(err)
	}
	s, _, meta := threadSurfaceOf(t, c, th.ID)
	if !meta.Tainted || !meta.Answers {
		t.Fatalf("meta %+v components %s", meta, s.Components)
	}
	if shown := c.threadShownCards(); shown[ref] != th.ID {
		t.Fatalf("the queue keeps its card: %v", shown)
	}
	body := actionOn("answer", s.ID, "thread_linked_0", `{"proposal":"`+ref+`","answer":"yes"}`)
	if code, b := postJSON(tw, "/api/espace/action", body, csrf); code != http.StatusNoContent {
		t.Fatalf("not answered on a tainted thread's surface: %d %s", code, b)
	}
}

// The brain holds a thread: it adds its part through its own tool, and
// reads the surface as one it may write to.
func TestThreadSurfaceBrainPart(t *testing.T) {
	c := startCore(t)
	th, err := c.openThread(threadIn{Title: "PR 12", Goal: "Merged and deployed."}, nil, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	sid := threadSurfaceID(th.ID)
	brain := connectServer(t, newMCPServer(c))
	out, msg := callOn(t, brain, "surface_write", map[string]any{"id": sid, "block": "etat", "components": []map[string]any{
		{"id": "root", "component": "Metric", "label": "Contrôles", "value": "12/12"}}})
	if msg != "" || out["block"] != "etat" {
		t.Fatalf("part %v %s", out, msg)
	}
	s, _, _ := threadSurfaceOf(t, c, th.ID)
	if !strings.Contains(string(s.Components), "12/12") || strings.Contains(string(s.Components), "Ajouté par") {
		t.Fatalf("not shown: %s", s.Components)
	}
	read, _ := callOn(t, brain, "workspace_read", map[string]any{})
	raw, _ := json.Marshal(read["threads"])
	if !strings.Contains(string(raw), `"id":"`+sid+`"`) || !strings.Contains(string(raw), `"yours":true`) || !strings.Contains(string(raw), `"blocks":["etat"]`) {
		t.Errorf("workspace_read: %s", raw)
	}
	if _, msg := callOn(t, brain, "surface_remove", map[string]any{"id": sid, "block": "etat"}); msg != "" {
		t.Fatal(msg)
	}
	if _, err := c.surface(sid); err != nil {
		t.Fatal("removing the part removed the surface")
	}
}

