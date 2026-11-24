package main

import (
	"fmt"
	"strings"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
)

// Who wanted a node (#678). Code records it when it makes the node, in
// its front matter's opened_by (nodefile.Origin), which no author writes:
//
//   - the brain, in a turn that answers messages of the user's: the user,
//     with the last of them (node_open, an answer filed in a new node);
//     in a turn of events only (a wake, an intention, a monitor's event):
//     an event;
//   - the user's yes on a card: the user, with the card;
//   - a skill's run: the skill, with what started it (its first event's
//     source, or its schedule);
//   - a task, a step: who wanted the work they belong to (the task's
//     maker, the parent's origin), with the task and the node;
//   - code, for one of Eriphe's own places (« Outils à revoir »): Eriphe,
//     not a node the user did not ask for.
//
// The page says it in its ⋯ menu; a node the user did not ask for has a
// sign on its bubble and in its header. A node made before has none, and
// its page says its origin is unknown.

// turnOrigin is who wants what the brain opens in its turn.
func (c *core) turnOrigin() *nodefile.Origin {
	if !c.turnAnswersUser() {
		return &nodefile.Origin{By: nodefile.OpenedEvent}
	}
	o := &nodefile.Origin{By: nodefile.OpenedUser}
	from, to := c.turn.span()
	c.store.db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM messages WHERE seq >= ? AND seq <= ? AND role = ?`, from, to, roleUser).Scan(&o.Msg)
	return o
}

// skillOrigin is a skill's run's: the skill, and what started it.
func skillOrigin(skill string, events []journalEvent) *nodefile.Origin {
	on := "schedule"
	if len(events) > 0 {
		on = events[0].Source
	}
	return &nodefile.Origin{By: nodefile.OpenedSkill, Skill: skill, On: on}
}

// originOf is a node's origin as its file says it; nil when it says none
// or there is no such node.
func (c *core) originOf(id string) *nodefile.Origin {
	if id == "" || c.nodes == nil {
		return nil
	}
	d, err := c.nodes.Read(id, 0)
	if err != nil {
		return nil
	}
	return d.Node.Meta.OpenedBy
}

// workOrigin is who wanted a node opened for the work of a node (a
// step, a task's child): that node's origin, with it (and the task that
// opens it); nil when it is not known.
func (c *core) workOrigin(node, task string) *nodefile.Origin {
	o := c.originOf(node)
	if o == nil {
		return nil
	}
	cp := *o
	cp.Node, cp.Task = node, task
	return &cp
}

// taskOrigin is who wanted what a task opens: its skill's run, or who
// wanted the task (its node's origin), with the task and the node it
// works in.
func (c *core) taskOrigin(task, node string) *nodefile.Origin {
	if r := c.runOfTask(task); r != nil {
		var events []journalEvent
		if len(r.Events) > 0 {
			if ev, ok := c.journalEvent(r.Events[0]); ok {
				events = append(events, ev)
			}
		}
		o := skillOrigin(r.Skill, events)
		o.Task, o.Node = task, node
		return o
	}
	if o := c.originOf(c.taskNode(task)); o != nil {
		cp := *o
		cp.Task, cp.Node = task, node
		return &cp
	}
	return nil
}

// pageOrigin is who wanted a node, as its page says it.
type pageOrigin struct {
	Unasked bool   `json:"unasked,omitempty"` // a skill or an event opened it: the sign
	Text    string `json:"text"`              // the sentence: « Ouvert à ta demande : « … » »
}

// nodeOriginLine is the sentence a page says of who wanted its node.
func (c *core) nodeOriginLine(o *nodefile.Origin, lang string) pageOrigin {
	if o == nil {
		return pageOrigin{Text: tr(lang, "origin.unknown")}
	}
	var text string
	switch o.By {
	case nodefile.OpenedUser:
		switch {
		case o.Card != "":
			text = tr(lang, "origin.user.card")
		case c.messageLine(o.Msg) != "":
			text = fmt.Sprintf(tr(lang, "origin.user.said"), c.messageLine(o.Msg))
		default:
			text = tr(lang, "origin.user")
		}
	case nodefile.OpenedSkill:
		text = fmt.Sprintf(tr(lang, "origin.skill"), o.Skill, originTrigger(o.On, lang))
	case nodefile.OpenedEriphe:
		text = tr(lang, "origin.eriphe")
	default:
		text = tr(lang, "origin.event")
	}
	return pageOrigin{Unasked: o.Unasked(), Text: text}
}

// originTrigger says what started a skill's run: its first event's
// source, or its schedule.
func originTrigger(on, lang string) string {
	kind, name, _ := strings.Cut(on, ":")
	switch {
	case on == "schedule" || on == "":
		return tr(lang, "origin.on.schedule")
	case kind == "monitor" && name != "":
		return fmt.Sprintf(tr(lang, "origin.on.monitor"), name)
	case kind == "alert" && name != "":
		return fmt.Sprintf(tr(lang, "origin.on.alert"), name)
	}
	return tr(lang, "origin.on.event")
}

// originLineMax is the most runes of the user's message a page quotes.
const originLineMax = 120

// messageLine is the first line of the user's message seq, "" when it is
// gone or holds no text.
func (c *core) messageLine(seq int) string {
	if seq <= 0 {
		return ""
	}
	var text string
	if c.store.db.QueryRow(`SELECT text FROM messages WHERE seq = ? AND role = ?`, seq, roleUser).Scan(&text) != nil {
		return ""
	}
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return truncate(strings.TrimSpace(line), originLineMax)
}
