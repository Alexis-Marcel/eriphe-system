package main

import (
	"fmt"
	"strings"
)

// A card read alone (#551): besides what it asks, an access card and a
// task's start card say where they come from (the thread, the task) and
// what their Oui does. Facts the code knows only, never words of the
// model's next to Oui and Non:
//
//   - the thread is the one of the item a calls card acts on
//     (item_cards.go), never a link the brain made (link_thread): a card
//     is not said to be a thread's on its word;
//   - a thread's title is shown only when no outside content reached the
//     thread (Thread.Tainted empty: its title is written once, at its
//     opening), else it is named by its number; a task's title only when
//     it was not started on a tainted turn (taskTaintKey);
//   - what Oui does follows what decideAccess or decideTaskStart do.
//
// It is read when a card is shown, not when its proposal is read.

// cardOrigin is where a card comes from and what its Oui does, in words.
type cardOrigin struct {
	Thread int64  // the thread it is raised for, 0 when none: its surface opens at /espace?open=thread-<id>
	From   string // the thread and the task that ask, "" when neither
	Yes    string // what Oui does, "" when the card does not say
}

// originParts are the facts a card's origin is said from.
type originParts struct {
	thread int64
	title  string // the thread's clean title
	task   string // the task's clean title, when the card's text does not name it
}

// from says the thread and the task in a line, in lang.
func (o originParts) from(lang string) string {
	var parts []string
	switch {
	case o.thread > 0 && o.title != "":
		parts = append(parts, fmt.Sprintf(tr(lang, "card.from.thread"), truncate(oneLine(o.title), 80)))
	case o.thread > 0:
		parts = append(parts, fmt.Sprintf(tr(lang, "card.from.threadn"), o.thread))
	}
	if o.task != "" {
		parts = append(parts, fmt.Sprintf(tr(lang, "card.from.task"), truncate(oneLine(o.task), 80)))
	}
	return strings.Join(parts, " · ")
}

// cleanThreadTitle is a thread's title when no outside content reached it,
// "" otherwise or when there is no such thread.
func (c *core) cleanThreadTitle(id int64) string {
	s, err := c.thread(id)
	if err != nil || s.Tainted != "" {
		return ""
	}
	return s.Title
}

// cleanTaskTitle is a task's title when it was not started on a tainted
// turn, "" otherwise.
func (c *core) cleanTaskTitle(id string) string {
	if id == "" || c.store.get(taskTaintKey(id)) != "" {
		return ""
	}
	t, err := c.store.task(id)
	if err != nil {
		return ""
	}
	return t.label()
}

// accessOrigin is an access card's origin: the task when its text does
// not name it (a call of a task other than the brain's, a skill's run);
// what its Oui does.
func (c *core) accessOrigin(a accessQuestion, lang string) cardOrigin {
	var o originParts
	if a.Call != "" && a.TaskID != c.brainTaskID() {
		o.task = c.cleanTaskTitle(a.TaskID) // a grant or a release names its task in its text
	}
	return cardOrigin{From: o.from(lang), Yes: c.accessYes(a, lang)}
}

// accessYes says what an access card's Oui does, as decideAccess does it.
func (c *core) accessYes(a accessQuestion, lang string) string {
	switch {
	case len(a.Refs) > 0:
		return fmt.Sprintf(tr(lang, "card.yes.release"), strings.Join(a.Refs, ", "))
	case strings.HasPrefix(a.Call, webSearchTool+" "):
		return fmt.Sprintf(tr(lang, "card.yes.search"), c.conf().assistantName())
	case strings.HasPrefix(a.Call, webFetchTool+" "):
		return fmt.Sprintf(tr(lang, "card.yes.page"), c.conf().assistantName())
	case a.Call != "":
		return tr(lang, "card.yes.call")
	}
	return fmt.Sprintf(tr(lang, "card.yes.grant"), verbs([]string{a.Need.Category}, lang), a.Need.TargetIn(lang))
}

// startOrigin is a task's start card's origin: the task (its text says
// only what it will use), what its Oui does.
func (c *core) startOrigin(taskID, lang string) cardOrigin {
	o := originParts{task: c.cleanTaskTitle(taskID)}
	return cardOrigin{From: o.from(lang), Yes: tr(lang, "card.yes.start")}
}
