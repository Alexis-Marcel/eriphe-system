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

// timelineOf is the steps the surface's Timeline shows: label → state, and
// each one's detail.
func timelineOf(data map[string]any) (map[string]string, map[string]string) {
	states, details := map[string]string{}, map[string]string{}
	items, _ := data["steps"].([]any)
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
	th, err := c.openThread(threadIn{Title: "PR 12", Goal: "Merged and deployed.", Subject: "pr:example/site#12"}, nil, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	sid, ref := threadSurfaceID(th.ID), fmt.Sprintf("thread:%d", th.ID)
	s, data, meta := threadSurfaceOf(t, c, th.ID)
	if s.Author != ref || s.Lifetime.While != ref || s.Title != "PR 12" || data["goal"] != "Merged and deployed." {
		t.Fatalf("surface %+v data %v", s, data)
	}
	if meta.Summary != "Ouvert" || !strings.Contains(meta.Why, "tenu à jour") || meta.Tainted || !meta.Answers {
		t.Errorf("meta %+v", meta)
	}
	if !strings.Contains(string(s.Components), "thread_nosteps") || !strings.Contains(data["state"].(string), "tenu par") {
		t.Errorf("components %s data %v", s.Components, data)
	}

	// Code's: the brain writes a part, never the whole, and removes nothing.
	whole := surfaceIn{ID: sid, Title: "Mine", Components: json.RawMessage(`[{"id":"root","component":"Text","text":"x"}]`)}
	if _, _, err := c.surfaceWrite(surfaceAuthorBrain, whole); err == nil {
		t.Error("the brain rewrote a thread's surface")
	}
	if err := c.surfaceRemove(sid, surfaceAuthorBrain); err == nil {
		t.Error("the brain removed a thread's surface")
	}
	if _, _, err := c.surfaceWrite("skill:revue", surfaceIn{ID: threadSurfaceID(th.ID + 1), Title: "x", Components: whole.Components}); err == nil {
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
	if meta.Summary != "Fait ✓" || data["summary"] != "Merged as 4f2c, deployed at 10:00." || s.Weight != threadWeightEnded {
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

// A led thread's surface follows it: its plan's card answered in place,
// then its steps on the Timeline as they move, a step that read outside
// content marked so (the surface tainted for the models, its cards still
// answered in place), the approval's card in place too, and the one-line
// summary for a small tile.
func TestThreadSurfaceFollowsSteps(t *testing.T) {
	c := startCore(t)
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	ctx := context.Background()
	th, err := c.openThread(threadIn{Title: "Brand name", Goal: "Find a name for the shop.", Steps: []stepSpec{
		{Key: "names", Title: "Find names", Prompt: "Propose ten names."},
		{Key: "ok", Title: "Sam's pick", Kind: stepApproval, Question: "Keep Lumen?", After: []string{"names"}},
	}}, nil, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	sid := threadSurfaceID(th.ID)
	asks := c.wholeThreadAsks(th.ID)
	if len(asks) != 1 {
		t.Fatalf("asks %v", asks)
	}
	plan := fmt.Sprintf("thread_ask_%d", asks[0])
	s, data, meta := threadSurfaceOf(t, c, th.ID)
	states, _ := timelineOf(data)
	if !strings.Contains(string(s.Components), `"id":"`+plan+`"`) || !strings.Contains(string(s.Components), `"proposalId":"`+proposalRef(cardThread, asks[0])+`"`) {
		t.Fatalf("no plan card on it: %s", s.Components)
	}
	if states["Find names"] != "pending" || states["Sam's pick"] != "pending" || meta.Summary != "une carte à toi" || s.Weight != queueSurfaceWeight(weightBlocksATask) {
		t.Errorf("proposed: %v %+v %d", states, meta, s.Weight)
	}

	answer := func(comp, ref string) (int, string) {
		return postJSON(tw, "/api/espace/action", actionOn("answer", sid, comp, `{"proposal":"`+ref+`","answer":"yes"}`), csrf)
	}
	if code, body := answer(plan, proposalRef(cardThread, asks[0])); code != http.StatusNoContent {
		t.Fatalf("plan answered in place: %d %s", code, body)
	}
	if th, _ = c.thread(th.ID); th.Phase != phaseRunning {
		t.Fatalf("plan not approved: %s", th.Phase)
	}
	s, data, _ = threadSurfaceOf(t, c, th.ID)
	if states, _ = timelineOf(data); states["Find names"] != "current" || strings.Contains(string(s.Components), plan) {
		t.Fatalf("running: %v %s", states, s.Components)
	}

	// The step's task read a web page: what it reports is marked external.
	names := stepOf(t, c, th, "names")
	c.store.set(taskTaintKey(names.Task), "read a web page")
	reports(t, c, names, receiptOf("Lumen, Rivet.", "names"))
	if _, err := c.settleStep(ctx, th.ID, "names", verdictAccept, "Lumen is free.", "", byBrain); err != nil {
		t.Fatal(err)
	}
	s, data, meta = threadSurfaceOf(t, c, th.ID)
	states, details := timelineOf(data)
	if states["Find names"] != "done" || states["Sam's pick"] != "current" ||
		!strings.HasPrefix(details["Find names"], "extérieur · Tâche") || !strings.Contains(details["Find names"], "Lumen is free.") {
		t.Fatalf("accepted: %v %v", states, details)
	}
	if !strings.Contains(s.Tainted, "step:names") || !meta.Tainted || !meta.Answers {
		t.Errorf("taint %q meta %+v", s.Tainted, meta)
	}
	if meta.Summary != "Find names ✓ · Sam's pick à toi" {
		t.Errorf("summary %q", meta.Summary)
	}
	ok := threadCard(t, c, askApproval)
	if !strings.Contains(string(s.Components), `"id":"thread_card_1"`) || !strings.Contains(string(s.Components), ok.Ref) {
		t.Fatalf("no approval card on it: %s", s.Components)
	}
	if code, body := answer("thread_card_1", ok.Ref); code != http.StatusNoContent {
		t.Fatalf("approval answered in place: %d %s", code, body)
	}
	_, data, _ = threadSurfaceOf(t, c, th.ID)
	if states, _ = timelineOf(data); states["Sam's pick"] != "done" {
		t.Fatalf("approved: %v", states)
	}
}

// A thread drawn from external content shows it, and its cards are
// answered in the conversation only.
func TestThreadSurfaceTainted(t *testing.T) {
	c := startCore(t)
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	th, err := c.openThread(threadIn{Title: "Invoice", Goal: "Pay the invoice the mail asks.", Steps: []stepSpec{
		{Key: "pay", Title: "Pay", Kind: stepApproval, Question: "Pay it?"},
	}}, []string{"mail:7"}, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	s, _, meta := threadSurfaceOf(t, c, th.ID)
	if !meta.Tainted || meta.Answers || !strings.Contains(string(s.Components), "thread_external") {
		t.Fatalf("meta %+v components %s", meta, s.Components)
	}
	ask := c.wholeThreadAsks(th.ID)[0]
	body := actionOn("answer", s.ID, fmt.Sprintf("thread_ask_%d", ask), `{"proposal":"`+proposalRef(cardThread, ask)+`","answer":"yes"}`)
	if code, b := postJSON(tw, "/api/espace/action", body, csrf); code != http.StatusBadRequest || !strings.Contains(b, "only ask") {
		t.Fatalf("answered on a tainted thread's surface: %d %s", code, b)
	}
}

// A step the user does shows « C'est fait »: its action names the thread
// and the step as code wrote them, and settles the step as the thread's
// page does; no author writes one.
func TestThreadSurfaceUserStep(t *testing.T) {
	c := startCore(t)
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	th, err := c.openThread(threadIn{Title: "Deploy", Goal: "The site runs the new version."}, nil, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	c.insertSteps(th.ID, []stepSpec{{Key: "deploy", Title: "Déploiement", Kind: stepUser}}, false)
	c.setStep(th.ID, "deploy", map[string]any{"status": stepRunning, "started": time.Now().Unix()})
	s, _, meta := threadSurfaceOf(t, c, th.ID)
	if !strings.Contains(string(s.Components), `"id":"thread_user_0_done"`) || !strings.Contains(string(s.Components), `"name":"stepDone"`) ||
		!strings.Contains(string(s.Components), "C'est fait") {
		t.Fatalf("no button: %s", s.Components)
	}
	if meta.Summary != "Déploiement à toi" {
		t.Errorf("summary %q", meta.Summary)
	}
	done := func(comp, ctx string) (int, string) {
		return postJSON(tw, "/api/espace/action", actionOn("stepDone", s.ID, comp, ctx), csrf)
	}
	right := fmt.Sprintf(`{"thread":%d,"step":"deploy"}`, th.ID)
	if code, b := done("thread_user_0_done", fmt.Sprintf(`{"thread":%d,"step":"other"}`, th.ID)); code != http.StatusBadRequest {
		t.Errorf("another step: %d %s", code, b)
	}
	if code, b := done("thread_user_0_label", right); code != http.StatusBadRequest {
		t.Errorf("not its button: %d %s", code, b)
	}
	if code, b := done("thread_user_0_done", right); code != http.StatusNoContent {
		t.Fatalf("stepDone: %d %s", code, b)
	}
	if st, err := c.step(th.ID, "deploy"); err != nil || st.Status != stepAccepted {
		t.Fatalf("step not settled: %+v %v", st, err)
	}
	after, _, _ := threadSurfaceOf(t, c, th.ID)
	if strings.Contains(string(after.Components), `"thread_user_0_done"`) {
		t.Errorf("the button stayed: %s", after.Components)
	}
	if code, b := done("thread_user_0_done", right); code != http.StatusBadRequest && code != http.StatusConflict {
		t.Errorf("done twice: %d %s", code, b)
	}
	if code, b := postJSON(tw, "/api/espace/action", actionOn("stepDone", "ailleurs", "x", right), csrf); code != http.StatusNotFound {
		t.Errorf("no surface: %d %s", code, b)
	}
	button := `[{"id":"root","component":"Button","label":"Fait","action":{"event":{"name":"stepDone","context":{"thread":1,"step":"deploy"}}}}]`
	if _, errs, err := c.surfaceWrite(surfaceAuthorBrain, surfaceIn{ID: "mine", Title: "Mine", Components: json.RawMessage(button)}); err == nil && len(errs) == 0 {
		t.Error("an author wrote stepDone")
	}
}

// Who holds a thread adds its part to the thread's surface (its lead
// here): shown under code's section, kept when code writes again, checked
// alone, marked external after reading outside content, woken when it is
// stale, taken away by surface_remove. Its wakes no longer ask it to
// write the surface.
func TestThreadSurfaceLeadPart(t *testing.T) {
	c, th := threadCore(t)
	ctx := context.Background()
	c.decideCard(ctx, threadCard(t, c, askPlan).ID, true, "web")
	th, _ = c.thread(th.ID)
	a := c.conf().Assistants[th.Lead]
	if strings.Contains(leadStartPrompt, "surface_write") || !strings.Contains(leadPrompt, "ajouter ta partie") {
		t.Error("the lead is still asked to write its thread's surface")
	}
	sid := threadSurfaceID(th.ID)
	lead := connectServer(t, c.assistantServer(a, nil))
	part := map[string]any{"id": sid, "title": "x", "components": []map[string]any{
		{"id": "root", "component": "Column", "children": []string{"diff", "pr"}},
		{"id": "diff", "component": "Code", "mode": "diff", "text": map[string]any{"@path": "/diff"}},
		{"id": "pr", "component": "Link", "label": "Ouvrir la PR", "url": "https://example.org/pr/12"},
	}, "data": map[string]any{"diff": "--- a\n+++ b\n@@ -1 +1 @@\n-x\n+y\n"}}
	out, msg := callOn(t, lead, "surface_write", part)
	if msg != "" || out["part"] != true || out["author"] != threadAuthor(th.ID) {
		t.Fatalf("part %v %s", out, msg)
	}
	s, data, _ := threadSurfaceOf(t, c, th.ID)
	if !strings.Contains(string(s.Components), `"thread_part"`) || !strings.Contains(string(s.Components), "Ajouté par son chef") ||
		!strings.Contains(string(s.Data), `"diff"`) || data["goal"] == nil || s.Tainted != "" {
		t.Fatalf("part not shown: %s %s", s.Components, s.Data)
	}

	// Code writes again: the part stays.
	reports(t, c, stepOf(t, c, th, "names"), receiptOf("Lumen.", "names"))
	if s, _, _ = threadSurfaceOf(t, c, th.ID); !strings.Contains(string(s.Components), `"thread_part"`) {
		t.Fatal("the part went with code's change")
	}

	// Checked alone.
	for name, bad := range map[string]map[string]any{
		"code's id":   {"id": sid, "title": "x", "components": []map[string]any{{"id": "root", "component": "Text", "text": "x"}, {"id": "thread_x", "component": "Text", "text": "y"}}},
		"code's data": {"id": sid, "title": "x", "components": oneText("x"), "data": map[string]any{"thread": 1}},
		"sources":     {"id": sid, "title": "x", "components": boundText("/n"), "sources": map[string]any{"/n": map[string]any{"tool": "ledger/balances"}}},
		"stepDone":    {"id": sid, "title": "x", "components": []map[string]any{{"id": "root", "component": "Button", "label": "Fait", "action": map[string]any{"event": map[string]any{"name": "stepDone", "context": map[string]any{"thread": th.ID, "step": "names"}}}}}},
	} {
		if _, msg := callOn(t, lead, "surface_write", bad); msg == "" {
			t.Errorf("%s: accepted", name)
		}
	}

	// After reading outside content: marked, no Proposal; its cards still answer in place.
	c.updateAssistant(a.Name, func(st *AssistantStatus) { st.Tainted = []string{"mail:7"} })
	withProposal := map[string]any{"id": sid, "title": "x", "components": []map[string]any{
		{"id": "root", "component": "Column", "children": []string{"p"}}, {"id": "p", "component": "Proposal", "proposalId": "rule:1"}}}
	if _, msg := callOn(t, lead, "surface_write", withProposal); !strings.Contains(msg, "Proposal") {
		t.Errorf("a tainted part placed a Proposal: %q", msg)
	}
	if _, msg := callOn(t, lead, "surface_write", map[string]any{"id": sid, "title": "x", "components": oneText("Lumen semble libre.")}); msg != "" {
		t.Fatal(msg)
	}
	s, _, meta := threadSurfaceOf(t, c, th.ID)
	if !strings.Contains(string(s.Components), "Ajouté par son chef · extérieur") || !strings.Contains(s.Tainted, "mail:7") || !meta.Answers {
		t.Errorf("tainted part: %s %q %+v", s.Components, s.Tainted, meta)
	}

	// Stale: the lead hears of its part.
	c.subscribeEvents(c.surfaceEventHeard)
	c.emit(journalEvent{Kind: evSurfaceStale, Key: sid, Source: "composer", Payload: map[string]any{"id": sid}})
	var text string
	c.store.db.QueryRow(`SELECT text FROM thread_inbox WHERE thread = ? ORDER BY id DESC LIMIT 1`, th.ID).Scan(&text)
	if !strings.Contains(text, "Ta partie") {
		t.Errorf("the lead was not told: %q", text)
	}

	if out, msg := callOn(t, lead, "surface_remove", map[string]any{"id": sid}); msg != "" || out["removed"] != "part" {
		t.Fatalf("remove %v %s", out, msg)
	}
	if s, _, _ = threadSurfaceOf(t, c, th.ID); strings.Contains(string(s.Components), "thread_part") {
		t.Error("the part stayed")
	}
	if err := c.stopThread(ctx, th.ID, byBrain); err != nil {
		t.Fatal(err)
	}
	if s, _, _ = threadSurfaceOf(t, c, th.ID); s.Lifetime.Until.IsZero() {
		t.Errorf("stopped: %+v", s.Lifetime)
	}
}

// A task step held for the user's yes shows its start card; the yes
// takes it away at once, as any change of the thread. An active thread
// whose surface went (made before, or removed) gets it back at the
// engine's tick, in place of a surface that had taken its id.
func TestThreadSurfaceHeldStartAndTick(t *testing.T) {
	c, th := threadCore(t)
	c.decideCard(context.Background(), threadCard(t, c, askPlan).ID, true, "web")
	names := stepOf(t, c, th, "names")
	c.store.db.Exec(`UPDATE tasks SET state = ?, desired = ? WHERE id = ?`, statePending, desiredHeld, names.Task)
	res, err := c.store.db.Exec(`INSERT INTO task_starts (task_id, needs, created) VALUES (?, '[]', ?)`, names.Task, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	start, _ := res.LastInsertId()
	c.keepThreadSurface(th.ID)
	s, _, meta := threadSurfaceOf(t, c, th.ID)
	if !strings.Contains(string(s.Components), proposalRef(cardTaskStart, start)) || meta.Summary != "Find names à toi" || s.Weight != queueSurfaceWeight(weightBlocksATask) {
		t.Fatalf("held start: %s %+v", s.Components, meta)
	}
	if err := c.decideTaskStart(start, true); err != nil {
		t.Fatal(err)
	}
	s, _, meta = threadSurfaceOf(t, c, th.ID)
	if strings.Contains(string(s.Components), proposalRef(cardTaskStart, start)) || strings.Contains(meta.Summary, "à toi") || s.Weight != threadWeight {
		t.Fatalf("after the yes: %s %+v", s.Components, meta)
	}

	// Gone, or its id taken by a lead's own before: the tick gives it back.
	c.surfaceRemove(s.ID, surfaceByUser)
	lead := fmt.Sprintf("lead:%d", th.ID)
	if _, _, err := c.writeSurface(lead, surfaceIn{ID: "lead-made", Title: "x", Components: json.RawMessage(`[{"id":"root","component":"Text","text":"x"}]`)}); err != nil {
		t.Fatal(err)
	}
	c.store.db.Exec(`UPDATE surfaces SET id = ? WHERE id = 'lead-made'`, s.ID)
	c.threadsTick(time.Now())
	if s, _, _ = threadSurfaceOf(t, c, th.ID); s.Author != threadAuthor(th.ID) {
		t.Fatalf("not given back: %+v", s)
	}
}

// The brain holds a light thread: it adds its part through its own
// tool, reads the surface as one it may write to, and a choice that
// waits for the user shows under « À toi ».
func TestThreadSurfaceBrainPart(t *testing.T) {
	c := startCore(t)
	th, err := c.openThread(threadIn{Title: "PR 12", Goal: "Merged and deployed."}, nil, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	sid := threadSurfaceID(th.ID)
	brain := connectServer(t, newMCPServer(c))
	out, msg := callOn(t, brain, "surface_write", map[string]any{"id": sid, "title": "x", "components": []map[string]any{
		{"id": "root", "component": "Metric", "label": "Contrôles", "value": "12/12"}}})
	if msg != "" || out["part"] != true {
		t.Fatalf("part %v %s", out, msg)
	}
	s, _, _ := threadSurfaceOf(t, c, th.ID)
	if !strings.Contains(string(s.Components), "12/12") || !strings.Contains(string(s.Components), "Ajouté par") {
		t.Fatalf("not shown: %s", s.Components)
	}
	read, _ := callOn(t, brain, "workspace_read", map[string]any{})
	raw, _ := json.Marshal(read["surfaces"])
	if !strings.Contains(string(raw), `"id":"`+sid+`"`) || !strings.Contains(string(raw), `"yours":true`) {
		t.Errorf("workspace_read: %s", raw)
	}
	if _, msg := callOn(t, brain, "surface_remove", map[string]any{"id": sid}); msg != "" {
		t.Fatal(msg)
	}
	if _, err := c.surface(sid); err != nil {
		t.Fatal("removing the part removed the surface")
	}

	c.insertSteps(th.ID, []stepSpec{{Key: "which", Title: "Le nom", Kind: stepChoice, Question: "Lequel ?", Options: []string{"Lumen", "Rivet"}}}, false)
	c.setStep(th.ID, "which", map[string]any{"status": stepRunning})
	s, _, meta := threadSurfaceOf(t, c, th.ID)
	if !strings.Contains(string(s.Components), `"thread_wait_0"`) || meta.Summary != "Le nom à toi" {
		t.Errorf("a choice that waits: %s %+v", s.Components, meta)
	}
}

// A call step's access card is shown in place while it waits, answered
// there, then kept under the steps with how the call went. On a tainted
// thread it is answered in the conversation only.
func TestThreadSurfaceCallStep(t *testing.T) {
	c := stepsCore(t)
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	th := lightThread(t, c)
	if _, err := c.addStep(context.Background(), th.ID, stepSpec{Key: "merge", Title: "Merge the PR", Kind: stepCall, Tool: mergeTool, Input: map[string]any{"number": 12}}, nil, "brain"); err != nil {
		t.Fatal(err)
	}
	st := stepOf(t, c, th, "merge")
	s, _, meta := threadSurfaceOf(t, c, th.ID)
	comps := string(s.Components)
	if !strings.Contains(comps, `"id":"thread_card_0","proposalId":"`+st.Card+`"`) || strings.Index(comps, "thread_card_0") > strings.Index(comps, `"thread_steps"`) ||
		meta.Summary != "Merge the PR à toi" || s.Weight != queueSurfaceWeight(weightBlocksATask) || !meta.Answers {
		t.Fatalf("call card: %s %+v", comps, meta)
	}
	answer := actionOn("answer", s.ID, "thread_card_0", `{"proposal":"`+st.Card+`","answer":"yes"}`)
	if code, b := postJSON(tw, "/api/espace/action", answer, csrf); code != http.StatusNoContent {
		t.Fatalf("answer: %d %s", code, b)
	}
	waitStep(t, c, th, "merge", stepAccepted)
	if state := c.cardState(st.Card); state != cardApproved {
		t.Errorf("card %s", state)
	}
	// The step's status is stored, then its thread's surface written again
	// (threadMoved), by the call's own goroutine: the surface follows.
	shown := func() bool {
		s, _, meta = threadSurfaceOf(t, c, th.ID)
		comps = string(s.Components)
		return strings.Contains(comps, `"thread_steps","thread_card_0"`) && strings.Contains(comps, st.Card) && !strings.Contains(meta.Summary, "à toi")
	}
	for deadline := time.Now().Add(5 * time.Second); !shown() && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	if !shown() {
		t.Errorf("after the yes: %s %+v", comps, meta)
	}

	// Tainted: shown, not answered in place.
	bad, err := c.openThread(threadIn{Title: "Mail", Goal: "Merge what the mail asks."}, []string{"mail:7"}, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.addStep(context.Background(), bad.ID, stepSpec{Key: "merge", Title: "Merge", Kind: stepCall, Tool: mergeTool, Input: map[string]any{"number": 13}}, nil, "brain"); err != nil {
		t.Fatal(err)
	}
	st = stepOf(t, c, bad, "merge")
	s, _, meta = threadSurfaceOf(t, c, bad.ID)
	if st.Card == "" || !strings.Contains(string(s.Components), st.Card) || meta.Answers {
		t.Fatalf("tainted call card: %+v %s %+v", st, s.Components, meta)
	}
	answer = actionOn("answer", s.ID, "thread_card_0", `{"proposal":"`+st.Card+`","answer":"yes"}`)
	if code, b := postJSON(tw, "/api/espace/action", answer, csrf); code != http.StatusBadRequest {
		t.Errorf("answered on a tainted thread: %d %s", code, b)
	}
	if state := c.cardState(st.Card); state != cardPending {
		t.Errorf("tainted card %s", state)
	}
}
