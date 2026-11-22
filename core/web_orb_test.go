package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// Where the orb speaks, only in reply to the user: Eriphe's answer where
// it belongs, a few plain lines beside a subject (more: its page says the
// rest), the home's answer whole; its words unasked (a greeting, an
// event's) are never the orb's; a failure says it, without its text; a new
// message of the user's moves on.
func TestOrbWords(t *testing.T) {
	tw := newTestWeb(t)
	c, st := tw.c, tw.c.store
	openGraph(t, c)
	d, err := c.nodes.Create(nodestore.New{Title: "Site en ligne", Body: "Il reste le certificat.\n", Author: byBrain})
	if err != nil {
		t.Fatal(err)
	}
	node := d.Node.Meta.ID

	// No message of the user's yet: Eriphe's words are not the orb's.
	st.addMessage(roleBrain, "Bonjour. Rien ne t'attend ce matin.", true)
	x, err := tw.w.orbFor("", true)
	if err != nil {
		t.Fatal(err)
	}
	if x.Answer != nil || x.Work != nil || x.Asked != 0 {
		t.Fatalf("the greeting: %+v", x)
	}

	// Asked in a subject: the answer belongs to it, a few lines, the rest
	// in its page.
	asked, _ := st.addNodeMessage(roleUser, "Où en est le certificat ?", "", node, true)
	c.turn.start(int(asked), int(asked), 0)
	said, _ := st.addNodeMessage(roleBrain, "Le certificat est **renouvelé**.\n\n## Ce que j'ai fait\n\n1. Lu le journal\n2. Relancé le service\n3. Noté la date", "", node, true)
	c.markAnswer(int(said))
	x, _ = tw.w.orbFor("", false)
	if x.Asked != int(asked) || x.Answer == nil || x.Answer.Node != node || x.Answer.Seq != int(said) || x.Answer.Whole || !x.Answer.More {
		t.Fatalf("the answer in a subject: %+v", x.Answer)
	}
	if x.Answer.Text != "Le certificat est renouvelé.\nCe que j'ai fait\nLu le journal" {
		t.Errorf("its lines: %q", x.Answer.Text)
	}
	if raw := fmtJSON(x); strings.Contains(raw, "**") || strings.Contains(raw, "Noté la date") {
		t.Errorf("the orb says more than a few plain lines: %s", raw)
	}
	// Written in a tainted turn: marked.
	st.set(taintKey(int(said)), "brain:task:t-1")
	if x, _ = tw.w.orbFor("", false); x.Answer == nil || !x.Answer.External {
		t.Errorf("a tainted answer is not marked: %+v", x.Answer)
	}
	// An event's turn later, at the home: neither its work, nor its words,
	// nor its failure are the orb's; the answer stays.
	ev, _ := st.addMessage(roleEvent, "Le catalogue d'outils a changé.", true)
	c.turn.goOn(int(ev), int(ev), 0, nil)
	if x, _ = tw.w.orbFor("Je regarde", true); x.Work != nil {
		t.Errorf("at work on an event: %+v", x.Work)
	}
	line, _ := st.addMessage(roleBrain, "Un outil de plus : films.", true)
	c.markAnswer(int(line))
	c.brainFailed("event turn broke", "turn")
	x, _ = tw.w.orbFor("", false)
	if x.Answer == nil || x.Answer.Seq != int(said) || x.Failed != nil || strings.Contains(fmtJSON(x), "films") {
		t.Fatalf("an event's words: %+v", x)
	}

	// The user writes again, not yet read: the answer is gone.
	next, _ := st.addMessage(roleUser, "Et le planning ?", false)
	if x, _ = tw.w.orbFor("", false); x.Answer != nil {
		t.Errorf("the answer outlived a new message: %+v", x.Answer)
	}
	// Read; Eriphe works on the subject, writing.
	st.db.Exec(`UPDATE messages SET delivered = 1 WHERE seq = ?`, next)
	c.turn.start(int(next), int(next), 0)
	c.turnNode(node)
	if x, _ = tw.w.orbFor("Je **regarde**.\n\nPuis", true); x.Work == nil || x.Work.Node != node || x.Work.Draft != "Je regarde." || x.Answer != nil {
		t.Errorf("at work: %+v", x)
	}
	// It fails: said, never its text.
	c.brainFailed("panic: raw trace", "turn")
	if x, _ = tw.w.orbFor("", false); x.Failed == nil || x.Answer != nil || strings.Contains(fmtJSON(x), "panic") {
		t.Errorf("failed: %+v", x)
	}
	// Answered at the home in more than a line: held whole, as Markdown.
	again, _ := st.addMessage(roleUser, "Tu as accès à mes films ?", true)
	c.turn.start(int(again), int(again), 0)
	whole := "Oui, je suis branché sur **Seerr** : je peux chercher un film.\n\nPour l'instant, chaque recherche te demande ton accord."
	home, _ := st.addMessage(roleBrain, whole, true)
	c.markAnswer(int(home))
	if x, _ = tw.w.orbFor("", false); x.Failed != nil || x.Answer == nil || !x.Answer.Whole || x.Answer.Text != whole || x.Answer.Node != "" {
		t.Errorf("the home's answer: %+v", x)
	}
	// A one-line answer at the home: its line.
	last, _ := st.addMessage(roleUser, "Allume la lumière.", true)
	c.turn.start(int(last), int(last), 0)
	done, _ := st.addMessage(roleBrain, "C'est allumé.", true)
	c.markAnswer(int(done))
	if x, _ = tw.w.orbFor("", false); x.Answer == nil || x.Answer.Whole || x.Answer.Text != "C'est allumé." {
		t.Errorf("a one-line answer: %+v", x.Answer)
	}
}

// Beside a subject the orb says at most a few lines, as plain text.
func TestFewLines(t *testing.T) {
	long := strings.Repeat("Une phrase assez longue pour remplir une bonne partie de la ligne. ", 3)
	for _, c := range []struct {
		in, want string
		cut      bool
	}{
		{"C'est allumé.", "C'est allumé.", false},
		{"Fait.\n\nLa vue est **prête**.", "Fait.\nLa vue est prête.", false},
		{"Un\nDeux\nTrois\nQuatre", "Un\nDeux\nTrois", true},
		{"```\ncode\n```\nC'est fait.", "C'est fait.", true},
		{"| a | b |\n|---|---|\nVoilà.", "Voilà.", true},
		{long, "Une phrase assez longue pour remplir une bonne partie de la ligne.", true},
		{"", "", false},
	} {
		got, cut := fewLines(c.in)
		if got != c.want || cut != c.cut {
			t.Errorf("fewLines(%q) = %q, %v; want %q, %v", c.in, got, cut, c.want, c.cut)
		}
	}
}

// The orb points at the newest card that waits in a node; a card no node
// holds waits above the bar.
func TestOrbWait(t *testing.T) {
	c, answer := netCore(t, "Tu peux garder un œil sur mes mails ?", "")
	out, err := c.brainNodeOpen(context.Background(), brainNodeOpenIn{Title: "Garder un œil sur mes mails", Body: "Ce qu'on suit.\n"})
	if err != nil {
		t.Fatal(err)
	}
	subject := out["id"].(string)
	if _, err := c.proposeView(proposeViewIn{Title: "Mes mails", Body: "Les mails.\n"}, nil); err != nil {
		t.Fatal(err)
	}
	answer("Dis oui sur sa carte.")
	w := &web{c: c}
	x, _ := w.orbFor("", false)
	if x.Wait == nil || x.Wait.Node != subject || len(x.Cards) != 0 {
		t.Fatalf("wait %+v, cards %v", x.Wait, x.Cards)
	}
	// The user writes again: the card waits in its page, the orb moves on.
	next, _ := c.store.addUserMessage("Et ensuite ?", nil, "")
	c.store.markDelivered(int(next))
	if x, _ := w.orbFor("", false); x.Wait != nil {
		t.Errorf("the orb still points at a card the user moved on from: %+v", x.Wait)
	}
	c.store.db.Exec(`DELETE FROM messages WHERE seq = ?`, next)
	if _, err := c.decideCard(context.Background(), x.Wait.Card, true, "web"); err != nil {
		t.Fatal(err)
	}
	if x, _ = w.orbFor("", false); x.Wait != nil {
		t.Errorf("a decided card still waits: %+v", x.Wait)
	}
	// A card raised in an event's turn waits in its page: the orb does
	// not go to it unasked.
	ev, _ := c.store.addMessage(roleEvent, "Un nouveau mail.", true)
	c.turn.goOn(int(ev), int(ev), 0, nil)
	c.turnNode(subject)
	if _, err := c.proposeView(proposeViewIn{Title: "Mes mails urgents", Body: "Les urgents.\n"}, nil); err != nil {
		t.Fatal(err)
	}
	if x, _ = w.orbFor("", true); x.Wait != nil {
		t.Errorf("the orb points at a card raised unasked, in the turn: %+v", x.Wait)
	}
	answer("Une carte de plus.")
	if x, _ = w.orbFor("", false); x.Wait != nil || x.Answer == nil || x.Answer.Text != "Dis oui sur sa carte." {
		t.Errorf("the orb points at a card raised unasked: %+v", x)
	}
	if ids, _ := c.cardsWhere(`state = ?`, cardPending); len(ids) != 1 || !c.nodeCardWaits(subject) {
		t.Errorf("the card raised unasked does not wait in its page: %v", ids)
	}
}

// A line of Eriphe's: the first that says something, as plain text, cut
// at its first sentence or a word; cut says it is not all of it.
func TestAckLine(t *testing.T) {
	long := "Voici ce que j'ai trouvé sur les trois devis du garage, avec ce qui change vraiment de l'un à l'autre. Le deuxième est le moins cher, et de loin."
	words := strings.Repeat("mot ", 60)
	for _, c := range []struct {
		in, want string
		cut      bool
	}{
		{"C'est allumé.", "C'est allumé.", false},
		{"  **Deux** rendez-vous : le [point produit](https://example.org/p) à 11 h.  ", "Deux rendez-vous : le point produit à 11 h.", false},
		{"## Le plan\n\n1. Pourquoi\n2. Comment", "Le plan", true},
		{"- `go test` passe\n", "go test passe", false},
		{"```\ncode\n```\nC'est fait.", "code", true},
		{"J'ai écrit mes questions dans le sujet.\n\nDis-moi.", "J'ai écrit mes questions dans le sujet.", true},
		{long, "Voici ce que j'ai trouvé sur les trois devis du garage, avec ce qui change vraiment de l'un à l'autre.", true},
		{words, strings.TrimSpace(strings.Repeat("mot ", 35)) + "…", true},
		{"", "", false},
	} {
		got, cut := ackLine(c.in)
		if got != c.want || cut != c.cut {
			t.Errorf("ackLine(%q) = %q, %v; want %q, %v", c.in, got, cut, c.want, c.cut)
		}
		if n := len([]rune(got)); n > ackRunes+1 {
			t.Errorf("ackLine(%q): %d runes", c.in, n)
		}
	}
}

// The workspace's stream sends the orb's event when it opens, then again
// as Eriphe writes (the draft) and once its reply lands, as JSON.
func TestWorkspaceStreamOrb(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	seq, _ := tw.c.store.addMessage(roleUser, "quel temps demain ?", true)
	tw.c.turn.start(int(seq), int(seq), 0)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", tw.srv.URL+"/events", nil)
	resp, err := tw.hc.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /events: %v", err)
	}
	defer resp.Body.Close()
	orbs := make(chan orbEvent, 20)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(nil, 1<<20)
		event := ""
		for sc.Scan() {
			line := sc.Text()
			if v, ok := strings.CutPrefix(line, "event: "); ok {
				event = v
				if event == "message" || event == "draft" || event == "exchange" {
					t.Errorf("event %s on the workspace's stream", event)
				}
			}
			if data, ok := strings.CutPrefix(line, "data: "); ok && event == "orb" {
				var ev orbEvent
				if json.Unmarshal([]byte(data), &ev) == nil {
					orbs <- ev
				}
			}
		}
		close(orbs)
	}()
	next := func(ok func(orbEvent) bool) orbEvent {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case ev, open := <-orbs:
				if !open {
					t.Fatal("stream closed")
				}
				if ok(ev) {
					return ev
				}
			case <-deadline:
				t.Fatal("no such orb event")
			}
		}
	}
	first := next(func(orbEvent) bool { return true })
	if first.Asked != int(seq) || first.Answer != nil || first.Work != nil {
		t.Fatalf("first orb event %+v", first)
	}
	tw.c.draft.set("Demain, il", int(seq))
	next(func(ev orbEvent) bool { return ev.Work != nil && ev.Work.Draft == "Demain, il" })
	said, _ := tw.c.store.addMessage(roleBrain, "Demain, il **pleut**.", true)
	tw.c.markAnswer(int(said))
	tw.c.draft.set("", int(seq)+1)
	got := next(func(ev orbEvent) bool { return ev.Answer != nil })
	if got.Answer.Text != "Demain, il pleut." || got.Work != nil {
		t.Fatalf("reply %+v", got)
	}
}

// A card is the orb's only when raised in answer to the user: shown with
// an answer of theirs, raised in a turn that answers them, or a call of
// the brain's in such a turn, whatever message of code's then showed it.
// A message of the user's sent while a turn runs waits for the next one:
// the answer then is the next turn's alone, not the words written meanwhile
// in reply to the message before it (waiting.spec.ts).
func TestOrbAnswerToTheLastMessage(t *testing.T) {
	tw := newTestWeb(t)
	c, st := tw.c, tw.c.store
	first, _ := st.addMessage(roleUser, "Prends ton temps.", true)
	c.turn.start(int(first), int(first), 0)
	waits, _ := st.addMessage(roleUser, "Et pour le budget ?", false)
	hello, _ := st.addMessage(roleBrain, "Me voilà.", true)
	c.markAnswer(int(hello))
	st.markDelivered(int(waits))
	c.turn.start(int(waits), int(waits), 0)
	if x, _ := tw.w.orbFor("", true); x.Answer != nil {
		t.Fatalf("the earlier answer taken for the reply to the message that waited: %+v", x.Answer)
	}
	ok, _ := st.addMessage(roleBrain, "D'accord.", true)
	c.markAnswer(int(ok))
	if x, _ := tw.w.orbFor("", false); x.Answer == nil || x.Answer.Text != "D'accord." || x.Answer.Seq != int(ok) {
		t.Fatalf("the reply to the message that waited: %+v", x.Answer)
	}
}

func TestOrbCardAsked(t *testing.T) {
	tw := newTestWeb(t)
	c := tw.c
	brief := convoCard{Ref: "access:1", Seq: 9}
	if c.cardAsked(brief, false) || c.cardAsked(convoCard{Ref: "view:2"}, false) {
		t.Fatal("a card raised unasked is the orb's")
	}
	if !c.cardAsked(convoCard{Ref: "view:2"}, true) {
		t.Error("a card raised in the turn that answers the user is not the orb's")
	}
	c.store.set(answerKey(9), "8")
	if !c.cardAsked(brief, false) {
		t.Error("a card shown with the answer is not the orb's")
	}
	ev, _ := c.store.addMessage(roleEvent, "Un mail.", true)
	c.turn.goOn(int(ev), int(ev), 0, nil)
	c.markCardAsked("access:3")
	if c.cardAsked(convoCard{Ref: "access:3", Seq: 12}, false) {
		t.Error("a call asked in an event's turn is the orb's")
	}
	asked, _ := c.store.addMessage(roleUser, "Réponds-lui.", true)
	c.turn.start(int(asked), int(asked), 0)
	c.markCardAsked("access:4")
	if !c.cardAsked(convoCard{Ref: "access:4", Seq: 12}, false) {
		t.Error("a call the brain asked a yes for, answering the user, is not the orb's")
	}
}
