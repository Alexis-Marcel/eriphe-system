package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The last exchange is the user's last message and Eriphe's lines after
// it: never an event's line nor an error's raw text, the last few replies.
func TestLastExchange(t *testing.T) {
	tw := newTestWeb(t)
	st := tw.c.store
	if got, err := st.lastExchange(""); err != nil || len(got) != 0 {
		t.Fatalf("empty conversation: %+v %v", got, err)
	}
	st.addMessage(roleBrain, "Bonjour Sam.", true)
	if got, _ := st.lastExchange(""); len(got) != 1 || got[0].Role != roleBrain {
		t.Fatalf("no message of the user's: Eriphe's last line, %+v", got)
	}
	st.addMessage(roleUser, "avant", true)
	st.addMessageVia(roleUser, "mets l'agenda en grand", "Telegram", true)
	st.addMessage(roleEvent, "<b>du dehors</b>", true)
	st.addMessageVia(roleError, "panic: raw trace", "turn", true)
	for _, s := range []string{"un", "deux", "trois"} {
		st.addMessage(roleBrain, s, true)
	}
	tainted, _ := st.addMessage(roleBrain, "lu dans un mail", true)
	st.set(taintKey(int(tainted)), "brain:task:t-1")
	st.addMessageVia(roleUser, "réglage", firstRunVia, true) // the first run's, hidden
	got, err := st.lastExchange("")
	if err != nil {
		t.Fatal(err)
	}
	var parts []string
	for _, l := range got {
		parts = append(parts, l.Role+":"+l.Text+":"+l.Via)
	}
	if strings.Join(parts, " ") != "user:mets l'agenda en grand:Telegram error:: brain:un: brain:deux: brain:trois: brain:lu dans un mail:" {
		t.Fatalf("exchange %v", parts)
	}
	if !got[len(got)-1].External || got[len(got)-2].External {
		t.Errorf("the line of a tainted turn is not marked: %+v", got)
	}
	st.addMessage(roleUser, "et demain ?", true)
	st.addMessageVia(roleError, "panic: raw trace", "turn", true)
	got, _ = st.lastExchange("")
	if len(got) != 2 || got[1].Role != roleError || got[1].Text != "" || got[1].Via != "" {
		t.Fatalf("an error, without its text: %+v", got)
	}
	// Many events and lines since: Eriphe's newest lines still come, the
	// last few.
	for range 60 {
		st.addMessage(roleEvent, "un événement", true)
		st.addMessage(roleBrain, "encore", true)
	}
	st.addMessage(roleBrain, "enfin", true)
	got, _ = st.lastExchange("")
	if len(got) != 1+exchangeReplies || got[0].Text != "et demain ?" || got[len(got)-1].Text != "enfin" {
		t.Fatalf("after many lines: %+v", got)
	}
}

// The bar shows one line of Eriphe's: the first that says something, as
// plain text, cut at its first sentence or a word; cut says the bar does
// not show it all.
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

// The bar's payload: the user's last message, Eriphe's first line only,
// never its Markdown nor the rest, never a reply to suggest; a failure
// says it, without its text.
func TestBarExchange(t *testing.T) {
	tw := newTestWeb(t)
	st := tw.c.store
	asked, _ := st.addMessage(roleUser, "Écris-moi un article sur le télétravail.", true)
	said, _ := st.addMessage(roleBrain, "J'ouvre le sujet **Écrire l'article**.\n\n## Le plan\n\n1. Ce qui a changé\n2. Ce qui reste", true)
	x, err := tw.w.exchangeFor("", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if x.Asked == nil || x.Asked.Seq != int(asked) || x.Asked.Text != "Écris-moi un article sur le télétravail." {
		t.Fatalf("asked %+v", x.Asked)
	}
	if x.Said == nil || x.Said.Seq != int(said) || x.Said.Text != "J'ouvre le sujet Écrire l'article." || x.Failed {
		t.Fatalf("said %+v", x.Said)
	}
	raw := fmtJSON(x)
	for _, never := range []string{"Le plan", "Ce qui reste", "**", "suggestions", "lines", "earlier"} {
		if strings.Contains(raw, never) {
			t.Errorf("the bar's payload carries %q: %s", never, raw)
		}
	}
	// Written in a tainted turn: marked.
	st.set(taintKey(int(said)), "brain:task:t-1")
	if x, _ = tw.w.exchangeFor("", "", "", false); x.Said == nil || !x.Said.External {
		t.Errorf("a tainted line is not marked: %+v", x.Said)
	}
	// The user's next message: no line of Eriphe's yet, then its draft's
	// first line while it writes.
	st.addMessage(roleUser, "Plus court.", true)
	if x, _ = tw.w.exchangeFor("", "", "Je le **raccourcis**.\n\nVoici", true); x.Said != nil || x.Draft != "Je le raccourcis." || !x.Thinking || x.Asked.Text != "Plus court." {
		t.Errorf("waiting: %+v", x)
	}
	st.addMessageVia(roleError, "panic: raw trace", "turn", true)
	if x, _ = tw.w.exchangeFor("", "", "", false); !x.Failed || x.Said != nil || strings.Contains(fmtJSON(x), "panic") {
		t.Errorf("failed: %+v", x)
	}
}

// An answer given at the home, more than a line, is held whole until the
// user writes again; a line an event made Eriphe say later never takes its
// place; an answer of one line is only its line.
func TestHomeAnswer(t *testing.T) {
	tw := newTestWeb(t)
	c, st := tw.c, tw.c.store
	asked, _ := st.addMessage(roleUser, "Tu as accès à mes films ?", true)
	c.turn.start(int(asked), int(asked), 0)
	whole := "Oui, je suis branché sur **Seerr** : je peux chercher un film, voir ce qui est demandé et en demander un. " +
		"Pour l'instant, chaque recherche te demande ton accord. Tu peux la mettre en accès libre dans Réglages → Outils → films."
	said, _ := st.addMessage(roleBrain, whole, true)
	c.markAnswer(int(said))
	x, err := tw.w.exchangeFor("", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if x.Answer == nil || x.Answer.Seq != int(said) || x.Answer.Text != whole || x.Said != nil || x.Asked == nil || x.Asked.Seq != int(asked) {
		t.Fatalf("the answer is not held whole: answer %+v, said %+v", x.Answer, x.Said)
	}
	// Written in a tainted turn: marked.
	st.set(taintKey(int(said)), "brain:task:t-1")
	if x, _ = tw.w.exchangeFor("", "", "", false); x.Answer == nil || !x.Answer.External {
		t.Errorf("a tainted answer is not marked: %+v", x.Answer)
	}

	// An event alone, later: its line shows, the answer stays.
	ev, _ := st.addMessage(roleEvent, "Le catalogue d'outils a changé.", true)
	c.turn.goOn(int(ev), int(ev), 0, nil)
	line, _ := st.addMessage(roleBrain, "Un outil de plus : films.", true)
	c.markAnswer(int(line))
	x, _ = tw.w.exchangeFor("", "", "", false)
	if x.Answer == nil || x.Answer.Seq != int(said) || x.Said == nil || x.Said.Seq != int(line) {
		t.Fatalf("an event's line took the answer's place: answer %+v, said %+v", x.Answer, x.Said)
	}
	// Nor is it held in a node's bar.
	if x, _ = tw.w.exchangeFor("n-1", "", "", false); x.Answer != nil {
		t.Errorf("a node's bar holds the home's answer: %+v", x.Answer)
	}

	// The user writes again, not yet read: the answer is gone.
	next, _ := st.addMessage(roleUser, "Allume la lumière.", false)
	if x, _ = tw.w.exchangeFor("", "", "", false); x.Answer != nil {
		t.Errorf("the answer outlived a new message: %+v", x.Answer)
	}
	// Read, and answered in one line: that line goes as any, nothing is held.
	st.db.Exec(`UPDATE messages SET delivered = 1 WHERE seq = ?`, next)
	c.turn.start(int(next), int(next), 0)
	done, _ := st.addMessage(roleBrain, "C'est allumé.", true)
	c.markAnswer(int(done))
	if x, _ = tw.w.exchangeFor("", "", "", false); x.Answer != nil || x.Said == nil || x.Said.Text != "C'est allumé." {
		t.Errorf("a one-line answer: answer %+v, said %+v", x.Answer, x.Said)
	}
}

// The workspace's stream sends the exchange when it opens, then again as
// Eriphe writes (the draft) and once its reply lands, as JSON.
func TestWorkspaceStreamExchange(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	seq, _ := tw.c.store.addMessage(roleUser, "quel temps demain ?", true)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", tw.srv.URL+"/events", nil)
	resp, err := tw.hc.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /events: %v", err)
	}
	defer resp.Body.Close()
	exchanges := make(chan exchangeEvent, 20)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(nil, 1<<20)
		event := ""
		for sc.Scan() {
			line := sc.Text()
			if v, ok := strings.CutPrefix(line, "event: "); ok {
				event = v
				if event == "message" || event == "draft" {
					t.Errorf("HTML event %s on the workspace's stream", event)
				}
			}
			if data, ok := strings.CutPrefix(line, "data: "); ok && event == "exchange" {
				var ev exchangeEvent
				if json.Unmarshal([]byte(data), &ev) == nil {
					exchanges <- ev
				}
			}
		}
		close(exchanges)
	}()
	next := func(ok func(exchangeEvent) bool) exchangeEvent {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case ev, open := <-exchanges:
				if !open {
					t.Fatal("stream closed")
				}
				if ok(ev) {
					return ev
				}
			case <-deadline:
				t.Fatal("no such exchange")
			}
		}
	}
	first := next(func(exchangeEvent) bool { return true })
	if first.Asked == nil || first.Asked.Text != "quel temps demain ?" || first.Said != nil || first.Draft != "" {
		t.Fatalf("first exchange %+v", first)
	}
	tw.c.draft.set("Demain, il", int(seq))
	next(func(ev exchangeEvent) bool { return ev.Draft == "Demain, il" })
	tw.c.store.addMessage(roleBrain, "Demain, il **pleut**.", true)
	tw.c.draft.set("", int(seq)+1)
	got := next(func(ev exchangeEvent) bool { return ev.Said != nil })
	if got.Said.Text != "Demain, il pleut." || got.Draft != "" {
		t.Fatalf("reply %+v", got)
	}
}
