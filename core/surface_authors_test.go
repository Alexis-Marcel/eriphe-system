package main

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"
)

func oneText(text string) []map[string]any {
	return []map[string]any{{"id": "root", "component": "Text", "text": text}}
}

// A session's lead keeps its session's surface: reminded at each wake
// while it has none, it writes it as lead:<session>, following the
// session by default, updates it by its id, hears when it is stale, and
// code removes it when the session ends.
func TestLeadSurfaceFollowsItsSession(t *testing.T) {
	c, s := sessionCore(t)
	ctx := context.Background()
	c.decideCard(ctx, sessionCard(t, c, askPlan).ID, true, "web")
	s, _ = c.session(s.ID)
	a := c.conf().Assistants[s.Lead]
	if !strings.Contains(leadPrompt, "surface_write") || !strings.Contains(leadStartPrompt, "surface_write") {
		t.Error("the lead is not told to keep its surface")
	}
	c.store.insertTask(&TaskRow{ID: "t-lead", Node: "nas", Agent: "sonnet", Runtime: "claude-code", Prompt: "x", State: stateIdle, Kind: assistantRoleLead})
	wake := func(text string) string {
		t.Helper()
		c.queueLead(s, text, nil)
		c.store.updateTask("t-lead", map[string]any{"state": stateIdle})
		if ok, err := c.deliverLead(ctx, a, &TaskRow{ID: "t-lead"}); !ok || err != nil {
			t.Fatalf("deliver: %v %v", ok, err)
		}
		items, _ := c.store.inbox("t-lead")
		return items[len(items)-1].Data["text"].(string)
	}
	if got := wake("L'étape names a rendu son travail."); !strings.Contains(got, "n'a pas encore sa surface") {
		t.Fatalf("no nudge without a surface: %q", got)
	}

	lead := connectServer(t, c.assistantServer(a, nil))
	out, msg := callOn(t, lead, "surface_write", map[string]any{"id": "session-nom", "title": "Nom de la boutique", "components": oneText("Étape 1 sur 4")})
	author := "lead:" + strconv.FormatInt(s.ID, 10)
	if msg != "" || out["author"] != author {
		t.Fatalf("write %v %s", out, msg)
	}
	sf, _ := c.surface("session-nom")
	if sf.Lifetime.While != "session:"+strconv.FormatInt(s.ID, 10) {
		t.Fatalf("lifetime %+v", sf.Lifetime)
	}
	if _, msg = callOn(t, lead, "surface_write", map[string]any{"id": "session-nom", "title": "Nom de la boutique", "components": oneText("Étape 2 sur 4")}); msg != "" {
		t.Fatal(msg)
	}
	if sf, _ = c.surface("session-nom"); !strings.Contains(string(sf.Components), "Étape 2") {
		t.Fatalf("not updated: %s", sf.Components)
	}
	if got := wake("L'étape market a rendu son travail."); strings.Contains(got, "n'a pas encore sa surface") {
		t.Errorf("nudged with a surface: %q", got)
	}

	// The composer finds it stale: its lead hears it.
	c.subscribeEvents(c.surfaceEventHeard)
	c.emit(journalEvent{Kind: evSurfaceStale, Key: "session-nom", Source: "composer", Payload: map[string]any{"id": "session-nom"}})
	var text string
	c.store.db.QueryRow(`SELECT text FROM session_inbox WHERE session = ? ORDER BY id DESC LIMIT 1`, s.ID).Scan(&text)
	if !strings.Contains(text, "« session-nom »") {
		t.Errorf("the lead was not told: %q", text)
	}

	if err := c.stopSession(ctx, s.ID, "brain"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.surface("session-nom"); err == nil {
		t.Error("the surface outlived its session")
	}
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
	if len(dropped) > 0 || c.attendSignals(kept, dropped, "skill:revue", false) != 1 {
		t.Fatalf("signal %v %v", kept, dropped)
	}
	if items := c.attentionOpen(); len(items) != 1 || items[0].Ref != "surface:revue-du-jour" || items[0].Weight != 2 {
		t.Fatalf("queue %+v", items)
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
	mustAdd(t, c.mem, &Memory{Kind: kindFact, Text: "Sam aime le thé vert", Status: memProposed, SourceKind: srcExtraction})
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
	c.attendOffers() // again: nothing doubles
	if n := len(c.attentionOpen()); n != 2 {
		t.Fatalf("%d items", n)
	}

	now := c.notifyTime()
	for _, at := range []string{breakAnswer, breakReturn, breakSession, breakCap} {
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
