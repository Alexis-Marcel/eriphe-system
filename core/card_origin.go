package main

import (
	"fmt"
	"strings"
)

// A card read alone (#551): besides what it asks, an access card and a
// task's start card say where they come from (the node, the task) and
// what their Oui does. Facts the code knows only, never words of the
// model's next to Oui and Non:
//
//   - the node is the one a calls card's calls belong to (item_cards.go):
//     the node whose item it acts on, else the node of the run that kept
//     them, never one a model names;
//   - a node's title and a task's are always shown: one that may be
//     someone else's words (TitleExternal; a task started on a tainted
//     turn, taskTaintKey) is marked so (card.from.ext) and the card says
//     it (External): never replaced, never passing for a trusted one;
//   - what Oui does follows what decideAccess or decideTaskStart do.
//
// It is read when a card is shown, not when its proposal is read.

// cardOrigin is where a card comes from and what its Oui does, in words.
type cardOrigin struct {
	Node string // the node it belongs to, "" when none
	From string // the node and the task that ask, "" when neither
	Yes  string // what Oui does, "" when the card does not say
	// External: From quotes words that may be someone else's, marked so
	External bool
}

// originParts are the facts a card's origin is said from.
type originParts struct {
	node     string
	title    string // the node's title
	titleExt bool   // it may be someone else's words
	task     string // the task's title, when the card's text does not name it
	taskExt  bool   // it may be someone else's words
}

// from says the node and the task in a line, in lang; external when it
// quotes words that may be someone else's (each marked so).
func (o originParts) from(lang string) (line string, external bool) {
	var parts []string
	switch {
	case o.node != "" && o.title != "":
		parts = append(parts, fmt.Sprintf(tr(lang, "card.from.node"), markedWords(o.title, o.titleExt, lang)))
	case o.node != "":
		parts = append(parts, tr(lang, "card.from.noden"))
	}
	if o.task != "" {
		parts = append(parts, fmt.Sprintf(tr(lang, "card.from.task"), markedWords(o.task, o.taskExt, lang)))
	}
	return strings.Join(parts, " · "), o.node != "" && o.title != "" && o.titleExt || o.task != "" && o.taskExt
}

// markedWords are a title in a line, followed by its mark when it may be
// someone else's words: shown, never replaced.
func markedWords(title string, external bool, lang string) string {
	s := truncate(oneLine(title), 80)
	if external {
		s += tr(lang, "card.from.ext")
	}
	return s
}

// markedTitle is markedWords of a title, "" for none.
func markedTitle(title string, external bool, lang string) string {
	if title == "" {
		return ""
	}
	return markedWords(title, external, lang)
}

// titleOfNode is a node's title and whether it may be someone else's words;
// "" when there is no such node.
func (c *core) titleOfNode(id string) (title string, external bool) {
	if c.nodes == nil {
		return "", false
	}
	sum, err := c.nodes.Get(id)
	if err != nil {
		return "", false
	}
	return sum.Title, sum.TitleExternal()
}

// shownNodeTitle is a node's title as a line of the bar shows it: followed
// by ⚠ when it may be someone else's words, never replaced; "" when there
// is no such node.
func (c *core) shownNodeTitle(id string) string {
	t, ext := c.titleOfNode(id)
	if ext && t != "" {
		return t + " ⚠"
	}
	return t
}

// titleOfTask is a task's title and whether it may be someone else's words
// (started on a tainted turn); "" when there is no such task.
func (c *core) titleOfTask(id string) (title string, external bool) {
	if id == "" {
		return "", false
	}
	t, err := c.store.task(id)
	if err != nil {
		return "", false
	}
	return t.label(), c.store.get(taskTaintKey(id)) != ""
}

// accessOrigin is an access card's origin: the task when its text does
// not name it (a call of a task other than the brain's, a skill's run);
// what its Oui does.
func (c *core) accessOrigin(a accessQuestion, lang string) cardOrigin {
	var o originParts
	// A brain's call, whichever of its tasks asked (a session that started
	// fresh since has another): not a task's.
	if t, err := c.store.task(a.TaskID); a.Call != "" && (err != nil || t.Kind != assistantRoleBrain) {
		o.task, o.taskExt = c.titleOfTask(a.TaskID) // a grant or a release names its task in its text
	}
	from, ext := o.from(lang)
	return cardOrigin{From: from, Yes: c.accessYes(a, lang), External: ext}
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
		return "" // its text says what it does; its exact call is under Détails
	}
	return fmt.Sprintf(tr(lang, "card.yes.grant"), verbs([]string{a.Need.Category}, lang), a.Need.TargetIn(lang))
}

// startOrigin is a task's start card's origin: the task (its text says
// only what it will use), what its Oui does.
func (c *core) startOrigin(taskID, lang string) cardOrigin {
	var o originParts
	o.task, o.taskExt = c.titleOfTask(taskID)
	from, ext := o.from(lang)
	return cardOrigin{From: from, Yes: tr(lang, "card.yes.start"), External: ext}
}
