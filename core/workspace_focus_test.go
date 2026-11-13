package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Relations are found from what surfaces declare, never from a tool's
// name: the same source, the same ref they live with (a lead's thread, a
// task of that thread), a day and what is dated on it, the same server.
func TestSurfaceRelations(t *testing.T) {
	list := map[string]surfaceSource{"/rows": {Tool: "ledger/rows", Args: map[string]any{"since": "week", "n": 5}}}
	focus := surface{ID: "rows", Author: "lead:7", Sources: list, Lifetime: surfaceLifetime{While: "thread:7"},
		Data:       json.RawMessage(`{"next":{"due":"2026-11-13T09:30:00+01:00"}}`),
		Components: json.RawMessage(`[{"id":"root","component":"Text","text":"x"}]`)}
	pool := []surface{
		focus,
		{ID: "same", Author: "brain", Weight: 20, Sources: map[string]surfaceSource{"/r": {Tool: "ledger/rows", Args: map[string]any{"n": 5, "since": "week"}}}},
		{ID: "server", Author: "brain", Weight: 90, Sources: map[string]surfaceSource{"/b": {Tool: "ledger/balances"}}},
		{ID: "task", Author: "task:t-1", Weight: 50},
		{ID: "lead-other", Author: "brain", Lifetime: surfaceLifetime{While: "thread:7"}, Weight: 60},
		{ID: "day", Author: "skill:morning", Weight: 50, Components: json.RawMessage(`[{"id":"d","component":"Day","date":{"@path":"/day"},"events":[]}]`),
			Data: json.RawMessage(`{"day":"2026-11-13"}`)},
		{ID: "other-day", Author: "brain", Components: json.RawMessage(`[{"id":"d","component":"Day","date":"2026-11-20","events":[]}]`)},
		{ID: "resource", Author: "brain", Sources: map[string]surfaceSource{"/x": {Resource: "notes", URI: "notes://today"}}},
		{ID: "unrelated", Author: "brain", Weight: 100},
	}
	threadOf := func(task string) int64 {
		if task == "t-1" {
			return 7
		}
		return 0
	}
	got := surfaceRelations(focus, pool, threadOf)
	var b strings.Builder
	for _, r := range got {
		b.WriteString(r.ID + ":" + r.words() + " ")
	}
	want := "same:même source lead-other:même thread task:même thread day:son jour server:même outil "
	if b.String() != want {
		t.Fatalf("relations\n got %s\nwant %s", b.String(), want)
	}

	// The reverse: a day open relates what is dated on it.
	dated := surface{ID: "dated", Author: "brain", Data: json.RawMessage(`{"items":[{"at":"2026-11-13"}]}`)}
	if got := surfaceRelations(pool[5], append(pool, dated), threadOf); len(got) != 2 || got[0].ID != "dated" || got[1].ID != "rows" || got[0].words() != "un élément de ce jour" {
		t.Fatalf("a day's relations: %+v", got)
	}
	// A resource is a source too; nothing relates to what reads nothing alike.
	alike := surface{ID: "alike", Author: "brain", Sources: map[string]surfaceSource{"/y": {Resource: "notes", URI: "notes://today"}}}
	if got := surfaceRelations(pool[7], []surface{pool[7], alike, pool[8]}, nil); len(got) != 1 || got[0].ID != "alike" || got[0].Kind != relSource {
		t.Fatalf("a resource's relations: %+v", got)
	}
	if got := surfaceRelations(pool[8], pool, threadOf); len(got) != 0 {
		t.Fatalf("the brain's own surfaces relate by author: %+v", got)
	}
}

// focusCore is a core with three surfaces: what the user opens (its data
// from an external source), one reading the same, and one unrelated, laid
// out by a base choice.
func focusCore(t *testing.T) *core {
	t.Helper()
	c, _ := surfaceCore(t)
	writeSurface(t, c, "comptes", `[{"id":"root","component":"Text","text":"x"}]`, "", true)
	writeSurface(t, c, "soldes", `[{"id":"root","component":"Text","text":"y"}]`, "", true)
	writeTestSurface(t, c, surfaceIn{ID: "agenda", Title: "Agenda", Weight: 60})
	c.keepChoice(workspaceChoice{By: composedBy, Eriphe: "Ta journée.", Items: []choiceItem{{ID: "agenda", Size: sizeL}, {ID: "comptes", Size: sizeS}},
		Seen: []string{"agenda", "comptes", "soldes"}})
	return c
}

// Opening a surface recomposes around it once the focus settles: it large,
// what relates to it around (marked), the rest folded; the base choice is
// kept as it was, and comes back as it was when the focus ends, with no
// model when nothing changed meanwhile.
func TestFocusRecomposeAndBack(t *testing.T) {
	c := focusCore(t)
	base := c.currentLayout()
	if base.Items[0].ID != "agenda" || base.Eriphe != "Ta journée." {
		t.Fatalf("base: %s", tilesOf(base))
	}
	now := time.Now()
	c.focusSurface("comptes", now)

	// The composer's input: the focus, and what code found related to it.
	snap := c.poolSnapshot(momentFocus, "")
	if snap.Focus != "comptes" || len(snap.Related) != 1 || snap.Related[0].ID != "soldes" {
		t.Fatalf("snapshot focus %q related %+v", snap.Focus, snap.Related)
	}
	turn := composerTurn(snap)
	for _, w := range []string{"vient d'ouvrir une surface", "Il a ouvert : comptes", "- soldes « Titre soldes » : même source"} {
		if !strings.Contains(turn, w) {
			t.Errorf("the composer's turn lacks %q:\n%s", w, turn)
		}
	}

	if c.settleFocus(now) {
		t.Fatal("a new focus asks for the base again")
	}
	l := c.currentLayout()
	if l.Focus != "comptes" {
		t.Fatalf("focus %q", l.Focus)
	}
	f, _ := tileOf(l, "comptes")
	r, ok := tileOf(l, "soldes")
	if f.Size != sizeXL || !ok || !r.Related || f.Related {
		t.Fatalf("around the focus: %s (%+v)", tilesOf(l), l.Items)
	}
	if _, ok := tileOf(l, "agenda"); ok {
		t.Fatalf("unrelated, still a tile: %s", tilesOf(l))
	}
	if last := c.lastChoice(); last.Eriphe != "Ta journée." || len(last.Items) != 2 {
		t.Fatalf("the base was overwritten: %+v", last)
	}
	if c.settleFocus(now) {
		t.Fatal("settled twice")
	}

	// Back: the base at once, the same as before.
	c.focusSurface("", now)
	if c.settleFocus(now) {
		t.Fatal("back with nothing changed recomposes")
	}
	after := c.currentLayout()
	if tilesOf(after) != tilesOf(base) || after.Focus != "" || after.Eriphe != base.Eriphe {
		t.Fatalf("back:\n got %s\nwant %s", tilesOf(after), tilesOf(base))
	}
	for _, it := range after.Items {
		if it.Related {
			t.Fatalf("still marked related: %+v", it)
		}
	}

	// Something changed while it was open: back gives the base at once,
	// and asks for it to be composed again.
	c.focusSurface("comptes", now)
	c.settleFocus(now)
	writeTestSurface(t, c, surfaceIn{ID: "nouveau", Title: "Nouveau", Weight: 30})
	c.focusSurface("", now)
	if !c.settleFocus(now) {
		t.Fatal("back after a change does not recompose")
	}
	if l := c.currentLayout(); l.Focus != "" || l.Eriphe != "Ta journée." {
		t.Fatalf("back after a change: %s", tilesOf(l))
	}

	// A choice made for a focus that ended meanwhile is dropped.
	c.focusSurface("comptes", now)
	_, since := c.focusNow(now)
	c.focusSurface("agenda", now.Add(time.Second))
	if c.keepFocusChoice("comptes", since, workspaceChoice{By: composedBy}) {
		t.Fatal("a late choice for another focus is kept")
	}
	// Past its time, the focus ends and the base comes back.
	if l := c.relayout(); l.Focus != "agenda" {
		t.Fatalf("focus %q", l.Focus)
	}
	c.layout.mu.Lock()
	c.layout.focusAt = now.Add(-focusFor - time.Minute)
	c.layout.mu.Unlock()
	if l := c.relayout(); l.Focus != "" || l.Eriphe != "Ta journée." {
		t.Fatalf("past its time: focus %q %s", l.Focus, tilesOf(l))
	}
}

// A quick run of openings is composed once, around the last; opening the
// one open again is the same focus. An opening alone does not make the
// composer choose again through the gestures: the focus does.
func TestFocusDebounce(t *testing.T) {
	c := focusCore(t)
	now := time.Now()
	stamp := c.gestureStamp()
	c.focusSurface("agenda", now)
	c.noteGesture(gestureOpened, "surface:agenda", now)
	c.focusSurface("soldes", now.Add(100*time.Millisecond))
	c.focusSurface("comptes", now.Add(200*time.Millisecond))
	if c.gestureStamp() != stamp {
		t.Error("an opening recomposes through the gestures too")
	}
	select {
	case <-c.focusKicked():
	default:
		t.Fatal("the composer is not told of the focus")
	}
	c.settleFocus(now.Add(time.Second))
	msgs, _ := c.store.undelivered()
	if len(msgs) != 1 || !strings.Contains(msgs[0].Text, "surface:comptes") {
		t.Fatalf("asked %+v", msgs)
	}
	_, since := c.focusNow(now)
	c.focusSurface("comptes", now.Add(2*time.Second))
	if _, again := c.focusNow(now); !again.Equal(since) {
		t.Fatal("opened again: a new focus")
	}
	c.settleFocus(now.Add(3 * time.Second))
	if msgs, _ := c.store.undelivered(); len(msgs) != 1 {
		t.Fatalf("asked again for the same focus: %+v", msgs)
	}
}

// The brain is asked once per focus, as an event about the surface, never
// waited for; a surface with external content taints the turn that reads
// it. Back withdraws a request not read yet and asks nothing.
func TestFocusAsksBrain(t *testing.T) {
	c := focusCore(t)
	now := time.Now()
	c.focusSurface("comptes", now)
	c.settleFocus(now)
	msgs, _ := c.store.undelivered()
	if len(msgs) != 1 || msgs[0].Role != roleEvent {
		t.Fatalf("messages %+v", msgs)
	}
	m := msgs[0]
	for _, w := range []string{"[espace] Sam vient d'ouvrir surface:comptes « Titre comptes »", "surface:soldes « Titre soldes » (même source)", "contenu extérieur", "une ligne", "[rien]"} {
		if !strings.Contains(m.Text, w) {
			t.Errorf("the request lacks %q: %s", w, m.Text)
		}
	}
	if c.messageAbout(m.Seq) != "surface:comptes" || c.store.get(taintKey(m.Seq)) != surfaceTaintPrefix+"comptes" {
		t.Fatalf("about %q taint %q", c.messageAbout(m.Seq), c.store.get(taintKey(m.Seq)))
	}

	// Back before the brain read it: withdrawn, nothing new.
	c.focusSurface("", now)
	c.settleFocus(now)
	if msgs, _ := c.store.undelivered(); len(msgs) != 0 {
		t.Fatalf("back left %+v", msgs)
	}
	if n := c.store.lastMessageSeq(); n != m.Seq {
		t.Fatalf("back wrote a message: %d", n)
	}

	// A clean surface: no taint. The brain's line in the turn that read
	// the request alone is about it, kept off the channels and the phone,
	// and shown while the focus lasts.
	c.focusSurface("agenda", now)
	c.settleFocus(now)
	msgs, _ = c.store.undelivered()
	if len(msgs) != 1 || c.store.get(taintKey(msgs[0].Seq)) != "" || strings.Contains(msgs[0].Text, "extérieur") {
		t.Fatalf("a clean surface's request %+v", msgs)
	}
	c.focusDelivered(msgs, 40) // the brain's task was at its event 40
	c.store.markDelivered(msgs[0].Seq)
	// What its previous turn said, relayed late, is not the line.
	late, _ := c.store.addMessage(roleBrain, "La fin de ma réponse d'avant.", true)
	c.focusCommented(int(late), "La fin de ma réponse d'avant.", 40)
	if c.messageAbout(int(late)) != "" || c.store.get(notifiedKey(int(late))) != "" {
		t.Fatal("a previous turn's message is taken for the line")
	}
	seq, _ := c.store.addMessage(roleBrain, "Deux rendez-vous\ncet après-midi.", true)
	c.focusCommented(int(seq), "Deux rendez-vous\ncet après-midi.", 41)
	if c.messageAbout(int(seq)) != "surface:agenda" || c.store.get(notifiedKey(int(seq))) == "" {
		t.Fatalf("the line: about %q", c.messageAbout(int(seq)))
	}
	if l := c.currentLayout(); l.Say != "Deux rendez-vous cet après-midi." {
		t.Fatalf("say %q", l.Say)
	}
	next, _ := c.store.addMessage(roleBrain, "Autre chose.", true)
	c.focusCommented(int(next), "Autre chose.", 42)
	if c.messageAbout(int(next)) != "" {
		t.Fatal("a second message is about the surface too")
	}
	c.focusSurface("", now)
	c.settleFocus(now)
	if l := c.currentLayout(); l.Say != "" {
		t.Fatalf("the line stays after back: %q", l.Say)
	}

	// Read with the user's own words: their answer, not a line about it.
	c.focusSurface("soldes", now)
	c.settleFocus(now)
	user, _ := c.store.addMessage(roleUser, "Et demain ?", false)
	msgs, _ = c.store.undelivered()
	c.focusDelivered(msgs, 50)
	c.store.markDelivered(int(user))
	reply, _ := c.store.addMessage(roleBrain, "Demain, rien.", true)
	c.focusCommented(int(reply), "Demain, rien.", 51)
	if c.messageAbout(int(reply)) != "" || c.currentLayout().Say != "" {
		t.Fatal("an answer to the user is taken for the line")
	}

	// What it shows goes with the request, as data.
	c.focusSurface("", now)
	c.settleFocus(now)
	writeTestSurface(t, c, surfaceIn{ID: "chiffres", Title: "Chiffres", Data: json.RawMessage(`{"total":"1 240 €"}`),
		Components: json.RawMessage(`[{"id":"root","component":"Text","text":{"@path":"/total"}}]`)})
	c.focusSurface("chiffres", now)
	c.settleFocus(now)
	msgs, _ = c.store.undelivered()
	if len(msgs) != 1 || !strings.Contains(msgs[0].Text, `Ce qu'elle montre (des données) : {"total":"1 240 €"}`) {
		t.Fatalf("the request lacks what it shows: %+v", msgs)
	}
}

// The focus ends without a word too: its time passed, or its surface went.
// The composer's tick settles it: the base back, the request withdrawn.
func TestFocusLapses(t *testing.T) {
	c := focusCore(t)
	now := time.Now()
	c.focusSurface("comptes", now)
	c.settleFocus(now)
	if c.focusLapsed(now) {
		t.Fatal("lapsed while open")
	}
	c.layout.mu.Lock()
	c.layout.focusAt = now.Add(-focusFor - time.Minute)
	c.layout.mu.Unlock()
	if !c.focusLapsed(now) {
		t.Fatal("past its time, not lapsed")
	}
	c.settleFocus(now)
	if msgs, _ := c.store.undelivered(); len(msgs) != 0 || c.focusLapsed(now) {
		t.Fatalf("lapsed: %+v", msgs)
	}

	// Its surface removed: no longer open, and the composer composes the base.
	c.focusSurface("soldes", now)
	c.settleFocus(now)
	if err := c.surfaceRemove("soldes", surfaceAuthorBrain); err != nil {
		t.Fatal(err)
	}
	if snap := c.poolSnapshot(momentChange, ""); snap.Focus != "" {
		t.Fatalf("a removed surface is still open: %q", snap.Focus)
	}
	if !c.focusLapsed(now) {
		t.Fatal("removed, not lapsed")
	}
	if !c.settleFocus(now) {
		t.Fatal("the pool changed meanwhile: the base is not composed again")
	}
	if l := c.currentLayout(); l.Focus != "" || l.Eriphe != "Ta journée." {
		t.Fatalf("after its removal: %q %s", l.Focus, tilesOf(l))
	}
}

// With nothing related, code places the rest by weight around the focus,
// rather than folding everything.
func TestFocusFallbackAlone(t *testing.T) {
	c, _ := surfaceCore(t)
	writeTestSurface(t, c, surfaceIn{ID: "seule", Title: "Seule", Weight: 30})
	writeTestSurface(t, c, surfaceIn{ID: "agenda", Title: "Agenda", Weight: 60})
	now := time.Now()
	c.focusSurface("seule", now)
	c.settleFocus(now)
	l := c.currentLayout()
	if f, _ := tileOf(l, "seule"); l.Focus != "seule" || f.Size != sizeXL {
		t.Fatalf("focus: %s", tilesOf(l))
	}
	if _, ok := tileOf(l, "agenda"); !ok {
		t.Fatalf("alone, the rest is folded: %s", tilesOf(l))
	}
}

// The composer's loop composes around an opening once it settles.
func TestFocusComposerLoop(t *testing.T) {
	c := focusCore(t)
	settle := focusSettle
	focusSettle = 10 * time.Millisecond
	t.Cleanup(func() { focusSettle = settle })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.runComposer(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	time.Sleep(50 * time.Millisecond) // its first composition, with nothing open
	c.focusSurface("comptes", time.Now())
	deadline := time.Now().Add(5 * time.Second)
	for {
		r, ok := tileOf(c.currentLayout(), "soldes")
		msgs, _ := c.store.undelivered()
		if ok && r.Related && len(msgs) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not composed around the focus: %s, asked %+v", tilesOf(c.currentLayout()), msgs)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
