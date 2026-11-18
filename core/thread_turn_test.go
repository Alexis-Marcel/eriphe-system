package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
)

// The brain's part is played here as its prompt asks it (brain-prompt.md,
// « Les fils »): the tools it calls, in order, and what eriphe-core
// makes of them.

// turnOnThread makes the brain's turn about a light thread of its own: a
// task the brain starts is a step of it.
func turnOnThread(t *testing.T, c *core) Thread {
	t.Helper()
	th, err := c.openThread(threadIn{Title: "Travail", Goal: "Le travail est fait.", Alias: "topic:travail"}, nil, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	c.turnThread(th.ID)
	return th
}

// timelineCards are the refs of the cards linked to a thread.
func timelineCards(t *testing.T, c *core, id int64) string {
	t.Helper()
	tl, err := c.threadTimeline(id)
	if err != nil {
		t.Fatal(err)
	}
	var refs []string
	for _, x := range tl.Cards {
		refs = append(refs, x.Ref)
	}
	return strings.Join(refs, " ")
}

// The prompt says when to open a thread, when its surface takes the stage,
// and how a wake turn goes (« fil » is the word the user reads).
func TestBrainPromptThreads(t *testing.T) {
	raw, _ := os.ReadFile("brain-prompt.md")
	prompt := string(raw)
	start := strings.Index(prompt, "## Les fils")
	end := strings.Index(prompt, "## Ta mémoire")
	if start < 0 || end < start {
		t.Fatal("no « Les fils »")
	}
	section := prompt[start:end]
	for _, want := range []string{
		"**Quand en ouvrir un.**", "`open_thread` avec sa clé", "reste du texte, sans fil", "tout ce qui se **montre** est un fil", "`take`", "`new_because`",
		"propose sa fusion", "seulement la vérification, rien d'autre", "Une tâche parle du fil de son tour",
		// Work in steps (#630): code runs and accepts them, the brain settles exceptions.
		"**Un travail en étapes.**", "`node_open` avec `steps`", "`[nœud <id> « titre »] …`", "`node_settle`", "sans te réveiller",
		"un **veilleur**", "`item_note`", "`note_item`", "`stop_thread`", "une recherche",
		"`workspace_focus` avec `thread-<id>`", "**sans** `workspace_focus`",
		"aucune question de toi-même", "`[rien]`", "son **but** est atteint",
		"se range seul dans sa chronologie", "Sa réponse sur une de ces cartes",
		// Working inside a thread (#607): a child for a matter of its own,
		// the part by named blocks, the bricks, the user's marks.
		"`open_thread` avec `parent`", "`find_thread`", "`block`", "`before`", "`surface_remove`", "jamais tout",
		"`Mark`", "`Grid`", "`\"kind\": \"stack\"`", "`Offers`", "n'appelle pas de réponse",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("« Les fils » lacks %q", want)
		}
	}
	// Its suggested replies, for any answer: its own words, none after reading outside.
	for _, want := range []string{"`<suggestions>`", "au plus 3", "sur un tour contaminé, eriphe-core n'en montre aucune"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt lacks %q", want)
		}
	}
	// What the epic replaced is gone: buttons drawn on a row by default.
	for _, gone := range []string{"composition par défaut", "en boutons sur sa ligne"} {
		if strings.Contains(prompt, gone) {
			t.Errorf("the prompts still say %q", gone)
		}
	}
}

// foundThreads are the threads find_thread gave.
func foundThreads(out map[string]any) []any {
	xs, _ := out["threads"].([]any)
	return xs
}

// cardItem is a card's item in the attention queue.
func cardItem(x convoCard) string { return "card:" + strconv.FormatInt(x.ID, 10) }

// The result of a call approved on a card linked to a thread comes back as
// that thread's event: the turn that reads it is about the thread. A card
// of no thread gives a turn about none.
func TestLinkedCallResultAboutThread(t *testing.T) {
	c := stepsCore(t)
	ctx := context.Background()
	c.store.insertTask(&TaskRow{ID: "b-1", Kind: "brain", Device: "box", Runtime: "claude-code", State: stateWaitingPermission})
	pr := lightThread(t, c)
	callBrainTool(t, c, "read_thread", map[string]any{"thread_id": pr.ID})
	brainCalls(t, c, "p-1", mergeTool, map[string]any{"number": 12})
	merge := accessCard(t, c, "merge")
	c.turn.about(0)
	brainCalls(t, c, "p-2", mergeTool, map[string]any{"number": 99})
	var alone convoCard
	xs, _ := c.cardsWhere(`kind = ? AND state = ? ORDER BY id`, cardAccess, cardPending)
	for _, x := range xs {
		if x.ID != merge.ID {
			alone = x
		}
	}

	deliver := func(x convoCard, approve bool, what string) string {
		t.Helper()
		c.store.markDelivered(c.store.lastMessageSeq())
		c.turn.about(0)
		if _, err := c.decideCard(ctx, x.ID, approve, "web"); err != nil {
			t.Fatal(err)
		}
		var msgs []Message
		waitFor(t, func() bool {
			msgs, _ = c.store.undelivered()
			return len(msgs) == 1 && strings.Contains(msgs[0].Text, what)
		})
		c.threadsDelivered(msgs)
		return msgs[0].Text
	}
	text := deliver(merge, true, "est exécutée")
	if c.turn.aboutThread() != pr.ID {
		t.Errorf("the result's turn is about %d, not the thread %d", c.turn.aboutThread(), pr.ID)
	}
	if !strings.HasPrefix(text, fmt.Sprintf("[thread %d « PR alerting »] ", pr.ID)) {
		t.Errorf("the result does not name its thread: %q", text)
	}
	if text := deliver(alone, false, "a refusé sur la carte"); strings.HasPrefix(text, "[thread") || c.turn.aboutThread() != 0 {
		t.Errorf("a card of no thread made the turn about %d: %q", c.turn.aboutThread(), text)
	}
}
