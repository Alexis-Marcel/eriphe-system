package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestSkillTriggerParse(t *testing.T) {
	front := func(extra string) []byte {
		return []byte("---\nname: x\ndescription: y\n" + extra + "---\nDo it.\n")
	}
	for _, ok := range []string{
		"", "when: monitor.new\n", "when: monitor.received key=getout\nevery: 15m\n",
		"when: time weekdays 08:00\nmodel: small\n", "when: monitor.new\ntools: [mail, calendar]\ncan: [open-thread]\n",
		"when: monitor.received key=maison status=couche\nevery: 1d\n",
		"when: monitor.failed, monitor.recovered key=getout-prod\n", "when: monitor.failed,monitor.recovered\n",
	} {
		if _, err := parseSkillMD(front(ok)); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"every: 15m\n", "when: Mail\n", "when: monitor.new key\n", "when: time\n", "when: time weekdays 08:00\nevery: 1h\n",
		"when: monitor.new\nevery: 2m\n", "when: monitor.new\nevery: 3d\n", "when: monitor.new\ncan: [delete-all]\n",
		"when: monitor.new\ntools: [Bad Name]\n",
		"when: monitor.new, time\n", "when: monitor.new, Mail\n", "when: a.b, c.d, e.f, g.h, i.j, k.l\n", "when: , key=x\n",
	} {
		if _, err := parseSkillMD(front(bad)); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	m, _ := parseSkillMD(front("when: monitor.received key=getout\nevery: 15m\ntools: [monitor]\ncan: [open-thread]\n"))
	tr := m.Trigger
	if !tr.matches(journalEvent{Kind: evMonitorReceived, Key: "GetOut"}) || tr.matches(journalEvent{Kind: evMonitorReceived, Key: "other"}) ||
		tr.matches(journalEvent{Kind: "monitor.new", Key: "getout"}) {
		t.Fatal("matches")
	}
	if s := tr.Text("fr"); !strings.Contains(s, "monitor.received") || !strings.Contains(s, "15 min") || !strings.Contains(s, "thread") {
		t.Fatalf("text %q", s)
	}
	// Several kinds share the filters; the run says each event's kind.
	m, _ = parseSkillMD(front("when: monitor.failed, monitor.recovered key=getout-prod\n"))
	for kind, want := range map[string]bool{evMonitorFailed: true, evMonitorRecovered: true, evMonitorChanged: false} {
		if m.Trigger.matches(journalEvent{Kind: kind, Key: "getout-prod"}) != want {
			t.Errorf("%s: %v", kind, !want)
		}
	}
	if m.Trigger.matches(journalEvent{Kind: evMonitorFailed, Key: "other"}) {
		t.Error("the filter is not shared")
	}
	m, _ = parseSkillMD(front("when: monitor.received key=getout status=firing\n"))
	if !m.Trigger.matches(journalEvent{Kind: evMonitorReceived, Key: "getout", Payload: map[string]any{"status": "firing"}}) ||
		m.Trigger.matches(journalEvent{Kind: evMonitorReceived, Key: "getout", Payload: map[string]any{"status": "resolved"}}) {
		t.Fatal("payload filters")
	}
}

// addTriggered adds and approves a skill, as the user does.
func addTriggered(t *testing.T, c *core, md string) {
	t.Helper()
	k, _, err := c.store.addSkillVersion([]skillFile{{Path: "SKILL.md", Data: []byte(md)}}, "upload")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.store.approveSkill(k.Name, k.Pending); err != nil {
		t.Fatal(err)
	}
}

func skillCore(t *testing.T) (*core, *time.Time) {
	c, _, now := pollCore(t)
	c.store.registerNode("pc", hashSecret("pc-secret"))
	c.store.setNodeStatus("pc", true, []byte(`{"runtimes":["claude-code"]}`))
	c.subscribeEvents(c.queueSkillEvent)
	return c, now
}

func runsOfSkill(t *testing.T, c *core, name string) []*skillRun {
	t.Helper()
	rs, err := c.skillRunsOf(name, 100)
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

// A batching skill: its events wait for the end of the period that the
// first one opened, then one task gets them all, tainted by what is
// external, lent the skill's tools, with no start card.
func TestSkillRunBatches(t *testing.T) {
	c, now := skillCore(t)
	addTriggered(t, c, "---\nname: mails-rdv\ndescription: les rendez-vous des mails\nwhen: monitor.new\nevery: 15m\ntools: [monitor]\n---\nRepère les rendez-vous.\n")
	ctx := context.Background()
	for i, subject := range []string{"RDV le 8", "Promo", "Facture"} {
		c.emit(journalEvent{Kind: "monitor.new", Key: "perso", Source: "monitor:mails", External: true,
			At: now.Add(time.Duration(i*5) * time.Minute), Payload: map[string]any{"subject": subject}})
	}
	c.emit(journalEvent{Kind: "calendar.changed", Source: "server:calendar", At: *now}) // not its kind
	c.tickSkills(ctx, now.Add(14*time.Minute))
	if rs := runsOfSkill(t, c, "mails-rdv"); len(rs) != 0 {
		t.Fatalf("before the period's end: %+v", rs)
	}
	c.tickSkills(ctx, now.Add(15*time.Minute))
	rs := runsOfSkill(t, c, "mails-rdv")
	if len(rs) != 1 || len(rs[0].Events) != 3 || rs[0].Task == "" || !rs[0].External || rs[0].State != skillRunOpen {
		t.Fatalf("runs %+v", *rs[0])
	}
	task, _ := c.store.task(rs[0].Task)
	for _, want := range []string{"Repère les rendez-vous", "RDV le 8", "Facture", "monitor.new"} {
		if !strings.Contains(task.Prompt, want) {
			t.Errorf("the prompt lacks %q:\n%s", want, task.Prompt)
		}
	}
	if d := desiredOf(t, c, task.ID); task.Tools != "monitor" || d != desiredRunning {
		t.Fatalf("tools %q, desired %q", task.Tools, d)
	}
	if len(c.taskTaint(task.ID)) == 0 {
		t.Fatal("a run on external events is not tainted")
	}
	if len(c.turnTaint()) != 0 {
		t.Fatal("the brain's turn was tainted")
	}
	c.tickSkills(ctx, now.Add(16*time.Minute))
	if rs := runsOfSkill(t, c, "mails-rdv"); len(rs) != 1 {
		t.Fatalf("started again: %+v", rs)
	}
}

// One open run per key: new events of its key wait, and its task gets them
// once its turn is over; then its last message is told, and it stops.
func TestSkillRunJoinsAndTells(t *testing.T) {
	c, now := skillCore(t)
	addTriggered(t, c, "---\nname: prod\ndescription: la prod\nwhen: monitor.received\n---\nEnquête.\n")
	ctx := context.Background()
	c.emit(journalEvent{Kind: evMonitorReceived, Key: "getout", Source: "alert:1", External: true, At: *now, Payload: map[string]any{"detail": "5xx"}})
	c.tickSkills(ctx, *now)
	rs := runsOfSkill(t, c, "prod")
	if len(rs) != 1 {
		t.Fatalf("%+v", rs)
	}
	id := rs[0].Task
	c.emit(journalEvent{Kind: evMonitorReceived, Key: "getout", Source: "alert:1", External: true, At: now.Add(time.Minute)})
	c.emit(journalEvent{Kind: evMonitorReceived, Key: "autre", Source: "alert:2", External: true, At: now.Add(time.Minute)})
	c.tickSkills(ctx, now.Add(time.Minute))
	if rs := runsOfSkill(t, c, "prod"); len(rs) != 2 || rs[0].Key != "autre" {
		t.Fatalf("another key, or the same one started again: %+v", rs)
	}
	// Its turn ends: it gets the waiting event of its key.
	c.store.updateTask(id, map[string]any{"state": stateIdle})
	if !c.skillTaskChanged(id, stateIdle, turnOutcome{}) {
		t.Fatal("not a run's task")
	}
	if r := c.runOfTask(id); len(r.Events) != 2 || r.State != skillRunOpen {
		t.Fatalf("joined %+v", r)
	}
	if items, _ := c.store.inbox(id); len(items) != 1 || !strings.Contains(items[0].Data["text"].(string), "nouveaux événements") {
		t.Fatalf("inbox %+v", items)
	}
	// Its next turn ends with a message: told, and the task stops.
	c.store.ackInbox(id, 1<<30)
	c.store.addEvents(id, []EventRow{{Seq: 1, Time: time.Now(), Type: "text", Data: map[string]any{"text": "GetOut renvoie des 5xx depuis 14h02, j'enquête."}}})
	c.store.updateTask(id, map[string]any{"state": stateIdle})
	c.skillTaskChanged(id, stateIdle, turnOutcome{})
	r := c.runOfTask(id)
	if r.State != skillRunDone || !strings.Contains(r.Note, "5xx") {
		t.Fatalf("run %+v", r)
	}
	ns, _ := c.notificationsWhere(`source = ?`, notifyFromSkill)
	if len(ns) != 1 || ns[0].Why != "Skill « prod »" || ns[0].Tainted == "" {
		t.Fatalf("notifications %+v", ns)
	}
	if d := desiredOf(t, c, id); d != desiredStopped {
		t.Fatalf("not stopped: %q", d)
	}
}

// A skill's message goes to the attention queue: without an attention
// block, as the notification it became, weighed by its prefix ([urgent]
// 5, plain 4, [brief] 3, [rien] nothing); with a block, the block wins.
func TestSkillRunAttention(t *testing.T) {
	c, now := skillCore(t)
	addTriggered(t, c, "---\nname: veille\ndescription: la veille\nwhen: monitor.new\n---\nRegarde.\n")
	ctx := context.Background()
	end := func(key, text string) *skillRun {
		t.Helper()
		c.emit(journalEvent{Kind: "monitor.new", Key: key, Source: "monitor:mails", At: *now})
		c.tickSkills(ctx, *now)
		var r *skillRun
		for _, x := range runsOfSkill(t, c, "veille") {
			if x.Key == key {
				r = x
			}
		}
		if r == nil {
			t.Fatalf("no run for %s", key)
		}
		c.store.addEvents(r.Task, []EventRow{{Seq: 1, Time: time.Now(), Type: "text", Data: map[string]any{"text": text}}, {Seq: 2, Time: time.Now(), Type: "turn_end"}})
		c.store.updateTask(r.Task, map[string]any{"state": stateIdle})
		c.skillTaskChanged(r.Task, stateIdle, turnOutcome{})
		return r
	}
	alertOf := func(r *skillRun) *attentionItem {
		t.Helper()
		ns, _ := c.notificationsWhere(`source = ? AND text LIKE ?`, notifyFromSkill, "%"+r.Key+"%")
		if len(ns) != 1 {
			return nil
		}
		return attentionOf(c, fmt.Sprintf("alert:%d", ns[0].ID))
	}
	for key, want := range map[string]int{"[urgent] Prod down, key a.": 5, "Prod slow, key b.": 4, "[brief] Prod fine, key c.": 3} {
		k := strings.TrimSuffix(key[strings.LastIndex(key, "key ")+4:], ".")
		it := alertOf(end(k, key))
		if it == nil || it.Weight != want || it.Source != "skill:veille" || !strings.Contains(it.Why, "Prod") {
			t.Errorf("%q: item %+v, want weight %d", key, it, want)
		}
	}
	before := len(c.attentionOpen())
	if end("d", "[rien]"); len(c.attentionOpen()) != before {
		t.Errorf("[rien] went to the queue: %+v", c.attentionOpen())
	}
	// A block wins over the prefix: its refs, its weights; no alert item.
	r := end("e", "[urgent] Une facture, key e.\n```json\n"+`{"receipt": {"status": "done", "attention": [`+
		`{"refs": ["mail:42"], "weight": 2, "why": "une facture à payer"}, {"refs": ["bogus:1"], "weight": 3, "why": "x"}]}}`+"\n```")
	if it := attentionOf(c, "mail:42"); it == nil || it.Weight != 2 || it.Source != "skill:veille" || it.Why != "une facture à payer" {
		t.Errorf("block item %+v", it)
	}
	if it := alertOf(r); it != nil {
		t.Errorf("the prefix went to the queue with a block: %+v", it)
	}
	if attentionOf(c, "bogus:1") != nil {
		t.Error("a bad ref was kept")
	}
	// A silent run with a block: nothing told, the block in the queue.
	end("f", "[rien]\n```json\n"+`{"receipt": {"status": "done", "attention": {"refs": ["agenda:2026-11-13"], "weight": 1, "why": "une journée calme"}}}`+"\n```")
	if it := attentionOf(c, "agenda:2026-11-13"); it == nil || it.Weight != 1 {
		t.Errorf("silent block item %+v", it)
	}
}

// [rien] tells nothing; a time trigger waits for its next time, then runs
// once for the times missed; a run's own end triggers nothing.
func TestSkillRunSilentAndTime(t *testing.T) {
	c, now := skillCore(t)
	addTriggered(t, c, "---\nname: brief\ndescription: le point du matin\nwhen: time daily 08:00\n---\nFais le point.\n")
	addTriggered(t, c, "---\nname: fins\ndescription: les fins de tâche\nwhen: task.ended\n---\nRegarde.\n")
	ctx := context.Background()
	c.tickSkills(ctx, *now) // 10:00: first sight
	c.tickSkills(ctx, now.Add(time.Hour))
	if rs := runsOfSkill(t, c, "brief"); len(rs) != 0 {
		t.Fatalf("ran before its time: %+v", rs)
	}
	c.tickSkills(ctx, now.Add(48*time.Hour)) // two 08:00 missed: one run
	rs := runsOfSkill(t, c, "brief")
	if len(rs) != 1 || rs[0].External {
		t.Fatalf("%+v", rs)
	}
	task, _ := c.store.task(rs[0].Task)
	if !strings.Contains(task.Prompt, "horaire") {
		t.Fatalf("prompt %s", task.Prompt)
	}
	c.store.addEvents(task.ID, []EventRow{{Seq: 1, Time: time.Now(), Type: "text", Data: map[string]any{"text": "[rien]"}}})
	c.store.updateTask(task.ID, map[string]any{"state": stateIdle})
	c.skillTaskChanged(task.ID, stateIdle, turnOutcome{})
	if r := c.runOfTask(task.ID); r.State != skillRunSilent {
		t.Fatalf("%+v", r)
	}
	c.taskEnded(task.ID, stateStopped)
	c.tickSkills(ctx, now.Add(49*time.Hour))
	if rs := runsOfSkill(t, c, "fins"); len(rs) != 0 {
		t.Fatalf("a run's end triggered a skill: %+v", rs)
	}
}

// A run that cannot start (an agent that does not exist) is kept refused.
func TestSkillRunRefused(t *testing.T) {
	c, now := skillCore(t)
	addTriggered(t, c, "---\nname: x\ndescription: y\nwhen: monitor.new\nmodel: nobody\n---\nz\n")
	c.emit(journalEvent{Kind: "monitor.new", Source: "monitor:mails", At: *now})
	c.tickSkills(context.Background(), *now)
	if rs := runsOfSkill(t, c, "x"); len(rs) != 1 || rs[0].State != skillRunRefused || rs[0].Note == "" {
		t.Fatalf("%+v", rs)
	}
}

// write_skill keeps a version waiting and puts it on a card; the yes
// approves it, then it runs; a tainted turn's card says so.
func TestWriteSkill(t *testing.T) {
	c, now := skillCore(t)
	out, err := c.writeSkill(writeSkillIn{Name: "mails-rdv", Description: "Les rendez-vous des mails",
		Instructions: "Repère les rendez-vous et propose de les ajouter.", When: "monitor.new", Every: "15m", Tools: []string{"monitor"}},
		[]string{"mail.read_message"})
	if err != nil {
		t.Fatal(err)
	}
	if out["skill"] != "mails-rdv" {
		t.Fatal(out)
	}
	var p proposal
	for _, x := range c.pendingProposals() {
		if x.Kind == cardSkill {
			p = x
		}
	}
	if !p.Pending || p.Tainted == "" || !strings.Contains(p.Text("fr"), "mails-rdv") || len(p.Fields) != 2 ||
		!strings.Contains(p.Fields[0].Value, "monitor.new") {
		t.Fatalf("card %+v %q", p, p.Text("fr"))
	}
	c.emit(journalEvent{Kind: "monitor.new", Source: "monitor:mails", At: *now})
	c.tickSkills(context.Background(), now.Add(time.Hour))
	if rs := runsOfSkill(t, c, "mails-rdv"); len(rs) != 0 {
		t.Fatal("ran before its approval")
	}
	if err := c.decideProposal(context.Background(), cardSkill, p.ID, true); err != nil {
		t.Fatal(err)
	}
	if k, _ := c.store.skill("mails-rdv"); !k.Active() {
		t.Fatalf("not approved: %+v", k)
	}
	if _, err := c.writeSkill(writeSkillIn{Name: "x", Description: "y", Instructions: "z", Tools: []string{"nowhere"}}, nil); err == nil {
		t.Fatal("a tool out of the catalog accepted")
	}
	if _, err := c.writeSkill(writeSkillIn{Name: "x", Description: "y", Instructions: "z", When: "Mail"}, nil); err == nil {
		t.Fatal("a bad trigger accepted")
	}
}
