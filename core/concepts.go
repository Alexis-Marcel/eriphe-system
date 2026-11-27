package main

import (
	"regexp"
	"strings"
)

// The brain's concepts, one per need, and the hints that send it to the
// right one when it reaches for another. The map is given to the brain
// once, in its tools' instructions (tools.go), which its standing prompt
// points to; the concept bench (concepts_test.go) checks it names every
// tool, and that each wrong tool for a request answers with the right one.

type concept struct {
	Name  string // intention, skill, …
	Tools string // its tools
	When  string // when to use it
}

var concepts = []concept{
	{"intention", "intend, intentions, update_intention", "something owed or to do once: at a date and time (remind me at 14:00), or the next time a name comes up"},
	{"skill", "write_skill, list_skills, read_skill", "what comes back or reacts on its own: a skill with when (an event: a mail, an alert, the user's return, a task's end; or a time) runs as a task in the background, in a node of its own or in the node it maintains (its home: a page it keeps up, line by line); its monitor may come on its card; also know-how a task is launched with (run_task skill)"},
	{"monitor", "write_monitor, monitors", "know that something changed in a tool that does not tell by itself (Grafana's alerts, a status page): eriphe-core calls it at an interval, without a model, and writes an event a skill reacts to; never a time skill that calls the tool"},
	{"rule", "rule", "what to do with notifications about a topic (now, brief, never, even at night, which lines), and the quiet hours"},
	{"setting", "setting", "how the user is told in general: device order, lines off, daily cap, time zone, a channel's detail, an urgent skill said aloud"},
	{"where", "where_now", "where a notification would reach the user now: lines, devices, the room a voice channel says they are in"},
	{"notify", "notify, notifications", "say something on your own, now or for the brief, about the turn's node"},
	{"since", "since", "what happened since a time, as eriphe-core recorded it"},
	{"task", "run_task, node_settle", "long or heavy work, by an agent on a machine, in a node of its own, a child of the node it is given; long work is cut by its agent into steps, child nodes that code runs and accepts on their proof: you settle only their exceptions ([nœud …] wakes)"},
	{"node", "node_open, node_find, node_read, node_write, node_edit, node_set, node_show, page_format", "the one object the user sees, a node of their graph, a Markdown page that holds all it shows (page_format): one per matter (a pull request, an appointment, a research), found again with node_find (archived ones too) before opening another; its doc says where the matter stands and links related nodes by title ([[Title]]), written on the version read (expect); set done only on its goal (a decision is kept), archived only by the user; a one-line answer stays text, a long one you file in its node (or code files it); a task works in a child node; long work of several steps runs as the steps of a node (node_settle on its exceptions)"},
}

const conceptMapEN = "intention, skill, monitor, rule, view, setting, where, notify, since, task, node"

var conceptLinesEN = func() string {
	var b strings.Builder
	for _, c := range concepts {
		b.WriteString("- " + c.Name + " (" + c.Tools + "): " + c.When + "\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}()

// The redirecting hints: a request that belongs to another concept.

// recurringRe: something that comes back on a schedule.
var recurringRe = regexp.MustCompile(`(?i)(tou(s|tes) les (jours|matins|soirs|semaines|mois|lundis|mardis|mercredis|jeudis|vendredis|samedis|dimanches|week-?ends|\d+ ?(h|heures|minutes|jours))|chaque (jour|matin|soir|semaine|mois|lundi|mardi|mercredi|jeudi|vendredi|samedi|dimanche|week-?end)|every (day|morning|evening|week|month|monday|tuesday|wednesday|thursday|friday|saturday|sunday|\d+ ?(h|hours|minutes|days))|\bdaily\b|\bweekly\b|quotidien|hebdomadaire)`)

const hintSkillNotIntend = "What comes back on a schedule is a skill: an intention fires once. Use write_skill with when: time and a schedule (time daily 07:00, time weekdays 08:00) when the user asks for it."

// intendHint is why an intention belongs elsewhere, or "".
func intendHint(text string) string {
	if recurringRe.MatchString(text) {
		return hintSkillNotIntend
	}
	return ""
}
