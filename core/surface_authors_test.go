package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func oneText(text string) []map[string]any {
	return []map[string]any{{"id": "root", "component": "Text", "text": text}}
}

// A skill reacts to surface.stale: by its when (id=<its surface>), and,
// whatever its when, about a surface it wrote, even a skill at fixed
// times; another's surface does not run it.
func TestSkillRewritesOnStale(t *testing.T) {
	m, err := parseSkillMD([]byte("---\nname: x\ndescription: x\nwhen: surface.stale id=revue\n---\nRéécris.\n"))
	if err != nil {
		t.Fatal(err)
	}
	st, _ := parseSkillTrigger(m)
	if !st.matches(journalEvent{Kind: evSurfaceStale, Key: "revue", Payload: map[string]any{"id": "revue"}}) ||
		st.matches(journalEvent{Kind: evSurfaceStale, Key: "autre", Payload: map[string]any{"id": "autre"}}) {
		t.Fatal("when: surface.stale id=… does not match its surface only")
	}

	c, now := skillCore(t)
	addTriggered(t, c, "---\nname: revue\ndescription: la revue du matin\nwhen: time daily 07:00\ntools: ["+workspaceServer+"]\n---\nÉcris la revue.\n")
	comps := []byte(`[{"id":"root","component":"Text","text":"Trois points"}]`)
	if _, _, err := c.surfaceWrite("skill:revue", surfaceIn{ID: "revue", Title: "Revue", Components: comps, Lifetime: surfaceLifetime{Kept: true}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.surfaceWrite("brain", surfaceIn{ID: "autre", Title: "Autre", Components: comps}); err != nil {
		t.Fatal(err)
	}
	c.emit(journalEvent{Kind: evSurfaceStale, Key: "autre", Source: "composer", At: *now, Payload: map[string]any{"id": "autre"}})
	c.tickSkills(context.Background(), *now)
	if rs := runsOfSkill(t, c, "revue"); len(rs) != 0 {
		t.Fatalf("another's surface ran it: %+v", rs)
	}
	c.emit(journalEvent{Kind: evSurfaceStale, Key: "revue", Source: "composer", At: *now, Payload: map[string]any{"id": "revue"}})
	c.tickSkills(context.Background(), *now)
	rs := runsOfSkill(t, c, "revue")
	if len(rs) != 1 || rs[0].State == skillRunRefused {
		t.Fatalf("runs %+v", rs)
	}
	task, _ := c.store.task(rs[0].Task)
	if task.Tools != workspaceServer || !strings.Contains(task.Prompt, evSurfaceStale) || !strings.Contains(task.Prompt, "même id") {
		t.Fatalf("task %q %q", task.Tools, task.Prompt)
	}
	if sk := c.triggeredSkills()[0]; !strings.Contains(sk.Trigger.Text("fr"), "périmée") {
		t.Errorf("its card does not say it: %q", sk.Trigger.Text("fr"))
	}

	// Stale again within the quiet: its author is not woken again.
	c.store.db.Exec(`UPDATE skill_runs SET state = ?`, skillRunDone)
	c.emit(journalEvent{Kind: evSurfaceStale, Key: "revue", Source: "composer", At: now.Add(time.Minute), Payload: map[string]any{"id": "revue"}})
	if q, _ := c.skillQueue("revue"); len(q) != 0 {
		t.Fatalf("woken again within the quiet: %+v", q)
	}
	c.emit(journalEvent{Kind: evSurfaceStale, Key: "revue", Source: "composer", At: now.Add(time.Hour), Payload: map[string]any{"id": "revue"}})
	if q, _ := c.skillQueue("revue"); len(q) != 1 {
		t.Fatalf("not woken after the quiet: %+v", q)
	}

	// A skill not lent the workspace is not woken by its surface.
	addTriggered(t, c, "---\nname: tri\ndescription: le tri\nwhen: time daily 08:00\n---\nTrie.\n")
	c.surfaceWrite("skill:tri", surfaceIn{ID: "tri", Title: "Tri", Components: comps})
	c.emit(journalEvent{Kind: evSurfaceStale, Key: "tri", Source: "composer", At: *now, Payload: map[string]any{"id": "tri"}})
	if q, _ := c.skillQueue("tri"); len(q) != 0 {
		t.Fatalf("a skill without the workspace woken: %+v", q)
	}
}

// Asked for something every morning, the brain writes a skill lent the
// workspace: a provided server is one of its tools, and its runs keep
// their surface.
func TestBrainRecurringRequestWritesWorkspaceSkill(t *testing.T) {
	c, now := skillCore(t)
	out, isErr := callBrainTool(t, c, "write_skill", map[string]any{"name": "matin", "description": "Le point du matin dans l'espace",
		"instructions": "Écris la surface du matin : l'agenda du jour.", "when": "time daily 07:00", "tools": []string{workspaceServer}})
	if isErr {
		t.Fatalf("write_skill: %v", out)
	}
	k, err := c.store.skill("matin")
	if err != nil || k.Pending == "" {
		t.Fatalf("skill %+v %v", k, err)
	}
	if err := c.store.approveSkill("matin", k.Pending); err != nil {
		t.Fatal(err)
	}
	var sk *triggeredSkill
	for _, x := range c.triggeredSkills() {
		if x.Name == "matin" {
			sk = &x
		}
	}
	if sk == nil || !containsStr(sk.Trigger.Tools, workspaceServer) {
		t.Fatalf("triggered %+v", sk)
	}
	c.startSkillRun(context.Background(), *sk, "", nil, *now)
	rs := runsOfSkill(t, c, "matin")
	if len(rs) != 1 || rs[0].State == skillRunRefused {
		t.Fatalf("runs %+v", rs)
	}
	if task, _ := c.store.task(rs[0].Task); task.Tools != workspaceServer || c.taskCaller(task.ID).author != "skill:matin" {
		t.Fatalf("task %q", task.Tools)
	}
	if !strings.Contains(brainPrompt, "tools: [eriphe-workspace]") {
		t.Error("the brain is not told how a recurring request keeps its surface")
	}
}

// surface.missing reaches the brain as an event of the conversation, once
// an hour for the same text, tainted when external.
func TestSurfaceMissingTellsTheBrain(t *testing.T) {
	c, now := skillCore(t)
	c.subscribeEvents(c.surfaceEventHeard)
	missing := func(text string, external bool) {
		c.emit(journalEvent{Kind: evSurfaceMissing, Source: "composer", At: *now, External: external, Payload: map[string]any{"text": text}})
	}
	missing("l'agenda de la semaine", false)
	if got := lastEvent(t, c); !strings.Contains(got, "[espace]") || !strings.Contains(got, "l'agenda de la semaine") {
		t.Fatalf("event %q", got)
	}
	msgs, _ := c.store.undelivered()
	n := len(msgs)
	missing("l'agenda de la semaine", false)
	if msgs, _ = c.store.undelivered(); len(msgs) != n {
		t.Error("told twice within the hour")
	}
	missing("les mails d'un inconnu", true)
	msgs, _ = c.store.undelivered()
	if last := msgs[len(msgs)-1]; c.store.get(taintKey(last.Seq)) == "" {
		t.Error("an external missing did not taint the brain's turn")
	}
	*now = now.Add(2 * time.Hour)
	missing("l'agenda de la semaine", false)
	if msgs, _ = c.store.undelivered(); len(msgs) != n+2 {
		t.Errorf("not told again after the hour: %d", len(msgs))
	}
}

// An item may be about a surface: surface:<id> is a ref of the queue,
// and it leaves with its surface.
func TestQueueSurfaceRefs(t *testing.T) {
	c, _ := surfaceCore(t)
	if !validAttentionRef("surface:revue-du-jour") || validAttentionRef("surface:Pas Un Id") {
		t.Fatal("surface refs")
	}
	if _, _, err := c.surfaceWrite("skill:revue", surfaceIn{ID: "revue-du-jour", Title: "Revue", Components: []byte(`[{"id":"root","component":"Text","text":"x"}]`)}); err != nil {
		t.Fatal(err)
	}
	kept, dropped := decodeSignals([]byte(`[{"refs": ["surface:revue-du-jour"], "weight": 2, "why": "La revue est prête"}]`))
	if len(dropped) > 0 || c.attendSignals(kept, dropped, "", "skill:revue", false) != 1 {
		t.Fatalf("signal %v %v", kept, dropped)
	}
	if items := c.attentionOpen(); len(items) != 1 || items[0].Ref != "surface:revue-du-jour" || items[0].Weight != 2 {
		t.Fatalf("queue %+v", items)
	}
	// Its own surface shows it: no surface of the queue's for it.
	if queueSurfaceID("surface:revue-du-jour") != "revue-du-jour" {
		t.Error("an item about a surface is not shown by it")
	}
	c.syncQueueSurfaces()
	if all, _ := c.surfaces(); len(all) != 1 {
		t.Fatalf("surfaces %+v", all)
	}
	c.surfaceRemove("revue-du-jour", surfaceByUser)
	if items := c.attentionOpen(); len(items) != 0 {
		t.Fatalf("an item outlived its surface: %+v", items)
	}
}

// Proposals waiting to be offered enter the queue at a low weight, for the
// composer: no breakpoint delivers them and the brain's turn does not list
// them; the brain still offers them, and once under its message their
// card is in front as before.
func TestOfferedProposalsInTheQueue(t *testing.T) {
	c, _ := notifyCore(t, at(t, "2026-11-04", "10:00"))
	mustAdd(t, c.mem, &Memory{Kind: kindFact, Text: "Sam aime le thé vert", Status: memProposed, SourceKind: srcTask})
	mustIntend(t, c.mem, &Intention{Text: "Appeler le garage", State: intentProposed, SourceKind: srcBrain, SourceRef: "msg:1", Trust: trustFirstParty})
	c.attendOffers()
	items := c.attentionOpen()
	if len(items) != 2 {
		t.Fatalf("queue %+v", items)
	}
	weights := map[string]int{}
	for _, it := range items {
		if it.Source != sourceOffer || !it.Held {
			t.Errorf("item %+v", it)
		}
		weights[strings.SplitN(it.Why, " : ", 2)[1]] = it.Weight
	}
	if weights["Sam aime le thé vert"] != weightOffered || weights["Appeler le garage"] != weightOfferedDue {
		t.Fatalf("weights %v", weights)
	}
	// The workspace shows them, as any item of the queue: a Proposal each,
	// in its group or alone.
	c.syncQueueSurfaces()
	for _, e := range c.queuePlan(time.Now()) {
		if sf, err := c.surface(e.surface); err != nil || !strings.Contains(string(sf.Components), "Proposal") {
			t.Errorf("no surface for %s: %v", e.it.Ref, err)
		}
	}
	c.attendOffers() // again: nothing doubles
	if n := len(c.attentionOpen()); n != 2 {
		t.Fatalf("%d items", n)
	}

	now := c.notifyTime()
	for _, at := range []string{breakAnswer, breakReturn, breakThread, breakCap} {
		if n := c.attentionBreak(at, now); n != 0 {
			t.Fatalf("%s delivered %d", at, n)
		}
	}
	if xs, _ := c.cardsWhere(`seq > 0`); len(xs) != 0 {
		t.Fatalf("cards put in the conversation: %+v", xs)
	}
	if b := c.attentionBlock(); b != "" {
		t.Fatalf("the brain's turn lists them: %q", b)
	}

	offer := c.offerCards(now)
	if !strings.Contains(offer, "à proposer : 2") {
		t.Fatalf("offer %q", offer)
	}
	seq := brainSays(c, "J'ai deux petites choses à te proposer.")
	if xs, _ := c.cardsWhere(`seq = ?`, seq); len(xs) != 2 {
		t.Fatalf("%d cards with the message", len(xs))
	}
	for _, it := range c.attentionOpen() {
		if it.Held || it.Weight != weightProposal || it.Source == sourceOffer {
			t.Errorf("offered, its item %+v", it)
		}
	}
}
