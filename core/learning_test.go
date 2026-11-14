package main

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func learnedAll(t *testing.T, m *memStore) []*learnEntry {
	t.Helper()
	xs, err := m.learnings(learnFilter{})
	if err != nil {
		t.Fatal(err)
	}
	return xs
}

// Every change to what steers Eriphe is an entry; the rest is not.
func TestLearningLogsMemoryChanges(t *testing.T) {
	m, _ := testMemory(t)
	mustAdd(t, m, &Memory{Kind: kindFact, Text: "Le NAS est au grenier"})
	mustAdd(t, m, &Memory{Kind: kindEpisode, Text: "Journée calme"})
	if xs := learnedAll(t, m); len(xs) != 0 {
		t.Fatalf("a fact or an episode is learned: %+v", xs[0])
	}
	lesson := mustAdd(t, m, &Memory{Kind: kindLesson, Subject: "owner", Text: "Sam veut des réponses courtes", SourceRef: "msg:3"})
	xs := learnedAll(t, m)
	if len(xs) != 1 {
		t.Fatalf("%d entries", len(xs))
	}
	e := xs[0]
	if e.Action != learnAdded || e.MemoryID != lesson.ID || e.Status != learnConfirmed || e.Source != learnFromUser || e.Risk != riskHigh ||
		e.Kind != kindLesson || len(e.Evidence) != 1 || e.Evidence[0] != "msg:3" || e.Text != lesson.Text {
		t.Fatalf("added: %+v", e)
	}
	// A preference replaced: changed, pointing at the one it replaced.
	pref := mustAdd(t, m, &Memory{Kind: kindPreference, Text: "Le tutoiement"})
	pref2 := mustAdd(t, m, &Memory{Kind: kindPreference, Text: "Le vouvoiement", Supersedes: pref.ID})
	if e = learnedAll(t, m)[0]; e.Action != learnChanged || e.MemoryID != pref2.ID || e.PrevID != pref.ID || e.Before != "Le tutoiement" ||
		e.Risk != riskLow {
		t.Fatalf("superseded: %+v", e)
	}
	// Changed in place, by the brain on the user's message.
	text := "Sam veut des réponses très courtes"
	if _, err := m.update(lesson.ID, memoryPatch{Text: &text, Ref: "msg:9"}, "brain", "précisé"); err != nil {
		t.Fatal(err)
	}
	if e = learnedAll(t, m)[0]; e.Action != learnChanged || e.Before != "Sam veut des réponses courtes" || e.Text != text ||
		e.Source != learnFromUser || e.Reason != "précisé" {
		t.Fatalf("updated: %+v", e)
	}
	// A proposal is not learned until approved; a rejected one never.
	prop := mustAdd(t, m, &Memory{Kind: kindDecision, Text: "Linux plutôt que macOS", Status: memProposed, SourceKind: srcExtraction,
		Trust: trustExternal, SourceRef: "task:t-1"})
	rej := mustAdd(t, m, &Memory{Kind: kindDecision, Text: "Rejetée", Status: memProposed, SourceKind: srcExtraction, Trust: trustFirstParty})
	n := len(learnedAll(t, m))
	m.forget(rej.ID, "owner", "rejected")
	if len(learnedAll(t, m)) != n {
		t.Fatal("a proposal is learned")
	}
	if _, err := m.approve(prop.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	if e = learnedAll(t, m)[0]; e.Action != learnAdded || e.Source != learnFromExternal || e.Status != learnConfirmed || e.Evidence[0] != "task:t-1" {
		t.Fatalf("approved: %+v", e)
	}
	// Forgotten.
	if err := m.forget(pref2.ID, "brain", "plus vrai"); err != nil {
		t.Fatal(err)
	}
	if e = learnedAll(t, m)[0]; e.Action != learnRemoved || e.MemoryID != pref2.ID || e.Status != learnTrial || e.Source != learnFromBrain {
		t.Fatalf("forgotten: %+v", e)
	}
	// Filters: kind, status, topic, subject.
	if xs, _ := m.learnings(learnFilter{Kinds: []string{kindLesson}}); len(xs) != 2 {
		t.Errorf("by kind: %d", len(xs))
	}
	if xs, _ := m.learnings(learnFilter{Statuses: []string{learnTrial}}); len(xs) != 1 {
		t.Errorf("by status: %d", len(xs))
	}
	if xs, _ := m.learnings(learnFilter{Query: "vouvoiement"}); len(xs) != 2 {
		t.Errorf("by topic: %d", len(xs))
	}
	if xs, _ := m.learnings(learnFilter{Subject: "owner"}); len(xs) != 2 {
		t.Errorf("by subject: %d", len(xs))
	}
	// An entry is fixed but for its status.
	if _, err := m.db.Exec(`UPDATE learning_log SET reason = 'x'`); err == nil {
		t.Fatal("an entry was rewritten")
	}
}

// Undoing each kind of entry, itself an entry.
func TestLearningUndoMemory(t *testing.T) {
	c, _ := testMemoryCore(t)
	m := c.mem
	last := func() *learnEntry { return learnedAll(t, m)[0] }
	undo := func(e *learnEntry) *learnEntry {
		t.Helper()
		r, err := c.unlearn(e.ID, "owner", "", nil)
		if err != nil {
			t.Fatalf("undo %d: %v", e.ID, err)
		}
		if r.Action != learnReverted || r.Reverts != e.ID {
			t.Fatalf("revert entry: %+v", r)
		}
		if got, _ := m.learning(e.ID); got.Status != learnReverted {
			t.Fatalf("not marked reverted: %+v", got)
		}
		return r
	}
	// Added: forgotten.
	x := mustAdd(t, m, &Memory{Kind: kindLesson, Text: "Toujours citer la source"})
	added := last()
	undo(added)
	if got, _ := m.memory(x.ID); got.Status != memDeleted {
		t.Fatalf("added memory: %s", got.Status)
	}
	if _, err := c.unlearn(added.ID, "owner", "", nil); err == nil {
		t.Fatal("undone twice")
	}
	if _, err := c.unlearn(last().ID, "owner", "", nil); err == nil {
		t.Fatal("an undo undone")
	}
	// Superseded: the old one is current again, in the profile again.
	old := mustAdd(t, m, &Memory{Kind: kindProfile, Text: "Sam habite à Lyon"})
	repl := mustAdd(t, m, &Memory{Kind: kindProfile, Text: "Sam habite à Nantes", Supersedes: old.ID})
	changed := last()
	// A newer change of the same thing comes first.
	more := "Sam habite à Nantes, quartier nord"
	if _, err := m.update(repl.ID, memoryPatch{Text: &more}, "owner", ""); err != nil {
		t.Fatal(err)
	}
	inPlace := last()
	if _, err := c.unlearn(changed.ID, "owner", "", nil); err == nil || !strings.Contains(err.Error(), "newer change") {
		t.Fatalf("older undone first: %v", err)
	}
	// Changed in place: back to the text before.
	undo(inPlace)
	if got, _ := m.memory(repl.ID); got.Text != "Sam habite à Nantes" {
		t.Fatalf("in place: %q", got.Text)
	}
	undo(changed)
	if got, _ := m.memory(repl.ID); got.Status != memDeleted {
		t.Fatal("the replacement stays")
	}
	got, _ := m.memory(old.ID)
	if !got.Current(m.now()) || got.SupersededBy != 0 || !got.Pinned {
		t.Fatalf("old not back: %+v", got)
	}
	if xs, _ := m.profile(); len(xs) != 1 || xs[0].ID != old.ID {
		t.Fatalf("profile: %v", ids(xs))
	}
	// Removed: restored.
	if err := m.forget(old.ID, "owner", ""); err != nil {
		t.Fatal(err)
	}
	undo(last())
	if got, _ := m.memory(old.ID); got.Status != memActive {
		t.Fatalf("removed: %s", got.Status)
	}
	// An undo made no other entry than its own.
	for _, e := range learnedAll(t, m) {
		if strings.HasPrefix(e.Who, whoUndo) {
			t.Fatalf("an undo logged as learning: %+v", e)
		}
	}
}

func TestLearningSkills(t *testing.T) {
	c, _ := testMemoryCore(t)
	s := c.store
	k, _, err := s.addSkillVersion(skillFiles(skillMD), "upload")
	if err != nil {
		t.Fatal(err)
	}
	v1 := k.Pending
	if err := c.approveSkill(k.Name, v1, "owner"); err != nil {
		t.Fatal(err)
	}
	e := learnedAll(t, c.mem)[0]
	if e.Kind != kindSkill || e.Action != learnAdded || e.Skill != k.Name || e.Version != v1 || e.Status != learnConfirmed {
		t.Fatalf("added: %+v", e)
	}
	k, _, _ = s.addSkillVersion(skillFiles(strings.Replace(skillMD, "then write", "then draft", 1)), "upload")
	v2 := k.Pending
	if err := c.approveSkill(k.Name, v2, "owner"); err != nil {
		t.Fatal(err)
	}
	changed := learnedAll(t, c.mem)[0]
	if changed.Action != learnChanged || changed.Version != v2 || changed.PrevVersion != v1 {
		t.Fatalf("changed: %+v", changed)
	}
	// Undo: the version before comes back, its bundle kept.
	if _, err := c.unlearn(changed.ID, "owner", "", nil); err != nil {
		t.Fatal(err)
	}
	if k, _ = s.skill(k.Name); k.Approved != v1 {
		t.Fatalf("after undo: %s", k.Approved)
	}
	if _, err := s.skillVersion(v1); err != nil {
		t.Fatal(err)
	}
	// Deleted, then undone: back.
	if err := c.deleteSkill(k.Name, "owner"); err != nil {
		t.Fatal(err)
	}
	removed := learnedAll(t, c.mem)[0]
	if removed.Action != learnRemoved || removed.Version != "" || removed.PrevVersion != v1 {
		t.Fatalf("removed: %+v", removed)
	}
	// The first entry is behind a newer one still in force.
	if _, err := c.unlearn(e.ID, "owner", "", nil); err == nil {
		t.Fatal("an old skill entry undone over a newer one")
	}
	if _, err := c.unlearn(removed.ID, "owner", "", nil); err != nil {
		t.Fatal(err)
	}
	if k, err = s.skill(k.Name); err != nil || k.Approved != v1 {
		t.Fatalf("restored: %+v %v", k, err)
	}
	// Every newer change undone, the first one can be: the skill goes.
	if _, err := c.unlearn(e.ID, "owner", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.skill(k.Name); err != errNoSkill {
		t.Fatalf("the added skill stays: %v", err)
	}
}

// unlearn: the user's message; on a tainted turn, only proposed.
func TestUnlearnTaint(t *testing.T) {
	c, _ := testMemoryCore(t)
	m := c.mem
	x := mustAdd(t, m, &Memory{Kind: kindPreference, Text: "Les réunions le matin"})
	e := learnedAll(t, m)[0]
	if _, err := c.brainUnlearn(unlearnIn{ID: e.ID, Reason: "demandé"}); err == nil {
		t.Fatal("undone without the user's message")
	}
	// Tainted, the user's message not asking: proposed, a card.
	seq := ownerSays(c, "Qu'as-tu lu dans ce mail ?")
	c.turn.taint("mail.read")
	out, err := c.brainUnlearn(unlearnIn{ID: e.ID, Reason: "demandé", SourceRef: ref(seq)})
	if err != nil || out["status"] != "proposed" {
		t.Fatalf("tainted: %v %v", out, err)
	}
	if got, _ := m.memory(x.ID); got.Status != memActive {
		t.Fatal("undone on a tainted turn")
	}
	p := c.proposalOf(cardUnlearn, e.ID)
	if !p.Pending || !strings.Contains(p.Text("fr"), "Les réunions le matin") {
		t.Fatalf("proposal: %+v %q", p, p.Text("fr"))
	}
	found := false
	for _, q := range c.pendingProposals() {
		found = found || q.Kind == cardUnlearn && q.ID == e.ID
	}
	if !found {
		t.Fatal("not among the pending proposals")
	}
	// The user approves it: undone.
	if err := c.decideProposal(context.Background(), cardUnlearn, e.ID, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.memory(x.ID); got.Status != memDeleted {
		t.Fatal("approved undo not applied")
	}
	// Clean turn with the user's message: applied at once.
	y := mustAdd(t, m, &Memory{Kind: kindDecision, Text: "Le train plutôt que l'avion"})
	e2 := learnedAll(t, m)[0]
	seq = ownerSays(c, "Oublie ça, finalement")
	out, err = c.brainUnlearn(unlearnIn{ID: e2.ID, Reason: "il a changé d'avis", SourceRef: ref(seq)})
	if err != nil || out["status"] != learnReverted {
		t.Fatalf("clean: %v %v", out, err)
	}
	if got, _ := m.memory(y.ID); got.Status != memDeleted {
		t.Fatal("not undone")
	}
	r := learnedAll(t, m)[0]
	if r.Who != "brain" || r.Evidence[0] != ref(seq) {
		t.Fatalf("revert entry: %+v", r)
	}
	// Tainted, but the user's message of the turn asks: applied, as for remember.
	z := mustAdd(t, m, &Memory{Kind: kindDecision, Text: "Le vélo plutôt que le bus"})
	e3 := learnedAll(t, m)[0]
	seq = ownerSays(c, "Oublie ce que tu as appris sur le vélo")
	c.turn.taint("mail.read")
	if out, err = c.brainUnlearn(unlearnIn{ID: e3.ID, Reason: "demandé", SourceRef: ref(seq)}); err != nil || out["status"] != learnReverted {
		t.Fatalf("asked in a tainted turn: %v %v", out, err)
	}
	if got, _ := m.memory(z.ID); got.Status != memDeleted {
		t.Fatal("not undone")
	}
	// learned lists them, an external memory named, not quoted.
	mustAdd(t, m, &Memory{Kind: kindDecision, Text: "SECRET d'un mail", Trust: trustExternal, SourceKind: srcExtraction, SourceRef: "task:t-9"})
	got, err := c.brainLearned(learnedIn{Kinds: []string{kindDecision}})
	if err != nil {
		t.Fatal(err)
	}
	views := got["learned"].([]learnedView)
	if len(views) != 5 || views[0].Text != "" || !views[0].External || views[0].What == "" {
		t.Fatalf("learned: %+v", views)
	}
	if _, err := c.brainLearned(learnedIn{Kinds: []string{"episode"}}); err == nil {
		t.Fatal("a kind that is never learned")
	}
}

func TestLearnedPage(t *testing.T) {
	tw, csrf := newMemoryWeb(t)
	m := tw.c.mem
	x := mustAdd(t, m, &Memory{Kind: kindLesson, Text: "Relire avant d'envoyer <b>x</b>"})
	e := learnedAll(t, m)[0]
	code, body, _ := tw.do("GET", "/settings/learned", nil, nil)
	if code != 200 {
		t.Fatalf("GET: %d %s", code, body)
	}
	for _, want := range []string{"a appris", "Relire avant d&#39;envoyer &lt;b&gt;x&lt;/b&gt;", "Leçon", "Confirmé", "Annuler", "/settings/learned/" + strconv.FormatInt(e.ID, 10) + "/undo", `href="/settings/learned" aria-current="page"`} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if _, body, _ = tw.do("GET", "/settings/learned?kind=skill", nil, nil); strings.Contains(body, "Relire avant") {
		t.Error("the kind filter shows a lesson as a skill")
	}
	if code, _, _ = tw.post("/settings/learned/"+strconv.FormatInt(e.ID, 10)+"/undo", url.Values{}, csrf); code != 200 {
		t.Fatalf("undo: %d", code)
	}
	if got, _ := m.memory(x.ID); got.Status != memDeleted {
		t.Fatal("not undone")
	}
	_, body, _ = tw.do("GET", "/settings/learned?status=reverted", nil, nil)
	if !strings.Contains(body, "Annulé") || !strings.Contains(body, "Relire avant") {
		t.Error("the undone entry is not shown")
	}
	// The undo is not offered twice.
	if _, _, h := tw.post("/settings/learned/"+strconv.FormatInt(e.ID, 10)+"/undo", url.Values{}, csrf); h.Get("HX-Refresh") != "" || h.Get("HX-Trigger") == "" {
		t.Fatal("undone twice")
	}
}

// The wake packet and the brief tell what was learned since the last
// brief, once; since lists it too.
func TestLearnedInHeartbeat(t *testing.T) {
	c, clock := testMemoryCore(t)
	m := c.mem
	c.store.set(lastBriefKey, strconv.FormatInt(clock.now().Add(-time.Hour).Unix(), 10))
	mustAdd(t, m, &Memory{Kind: kindPreference, Subject: "owner", Text: "Sam préfère les messages du soir", Trust: trustFirstParty, SourceKind: srcBrain})
	mustAdd(t, m, &Memory{Kind: kindDecision, Text: "Ce que Sam a écrit lui-même"}).Trust = trustOwner
	m.db.Exec(`UPDATE learning_log SET status = status`) // a status change is allowed
	p, err := c.wakePacket()
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Learned) != 2 {
		t.Fatalf("learned: %d", len(p.Learned))
	}
	text := p.render("Sam")
	if !strings.Contains(text, "Ce que tu as appris depuis le dernier point") || !strings.Contains(text, "les messages du soir") ||
		!strings.Contains(text, "une phrase chacun") {
		t.Fatalf("render:\n%s", text)
	}
	c.learnedTold(p.Learned)
	if p, _ = c.wakePacket(); len(p.Learned) != 0 {
		t.Fatal("told twice")
	}
	mustAdd(t, m, &Memory{Kind: kindLesson, Text: "Une nouvelle leçon"})
	items, _, err := c.feed(clock.now().Add(-time.Hour), []string{feedLearned}, 50)
	if err != nil || len(items) != 3 || items[0].Ref == "" || !strings.Contains(items[2].summary("fr", true), "Une nouvelle leçon") {
		t.Fatalf("feed: %+v %v", items, err)
	}
}
