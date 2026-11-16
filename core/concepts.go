package main

import (
	"regexp"
	"strings"
	"sync"
)

// The brain's concepts, one per need, and the hints that send it to the
// right one when it reaches for another. The map is given to the brain in
// the MCP instructions (here, in English) and at the top of its standing
// prompt (brain-prompt.md, « Tes concepts », in French); the concept bench
// (concepts_test.go) checks both name every tool, and that each wrong
// tool for a request answers with the right one.

type concept struct {
	Name  string // memory, intention, …
	Tools string // its tools
	When  string // when to use it
}

var concepts = []concept{
	{"memory", "remember, recall", "what you must know about the user: facts, people, decisions, how they want you to talk (where a matter stands is its thread's summary)"},
	{"intention", "intend, intentions, update_intention", "something owed or to do once: at a date and time (remind me at 14:00), or the next time a name comes up"},
	{"skill", "write_skill, list_skills, read_skill", "what comes back or reacts on its own: a skill with when (an event: a mail, an alert, the user's return, a task's end; or a time) runs as a task in the background and works on its thread; a source the user wants followed continuously gets a watcher, a skill that keeps its standing thread (thread: its alias, tools: eriphe-threads); also know-how followed when asked"},
	{"monitor", "write_monitor, monitors", "know that something changed in a tool that does not tell by itself (Grafana's alerts, a status page): eriphe-core calls it at an interval, without a model, and writes an event a skill reacts to; never a time skill that calls the tool"},
	{"rule", "rule", "what to do with notifications about a topic (now, brief, never, even at night, which lines), and the quiet hours"},
	{"workspace", "workspace_arrange, workspace_focus", "the workspace, ordered by code (what waits for the user, then what is urgent, then what is new): arrange its surfaces as the user asks (a size, a place, kept until placed again; a preference about its layout is applied so); put on its stage the surface that answers what they asked from it (workspace_focus); what they want to see more or less of is a preference memory about topic:workspace, which whoever notes a thread's items reads"},
	{"surface", "surface_write, surface_remove, workspace_read, catalog_read", "your part of a thread's tile, thread-<id>, under what eriphe-core keeps there (find or open the thing's thread first; a one-off thing to see is a day thread, open_thread ends day): live from any tool you read freely (sources), or composed after a task step of the thread; no tile of your own; components are shapes, never tool names"},
	{"setting", "setting", "how the user is told in general: device order, lines off, daily cap, time zone, a channel's detail, an urgent skill said aloud"},
	{"where", "where_now", "where a notification would reach the user now: lines, devices, the room a voice channel says they are in"},
	{"notify", "notify, notifications", "say something on your own, now or for the brief, about the turn's thread"},
	{"since", "since", "what happened since a time, as eriphe-core recorded it"},
	{"task", "run_task", "long or heavy work, by an agent on a machine: a step of the thread the turn is about"},
	{"thread", "open_thread, find_thread, read_thread, name_thread, close_thread, reopen_thread", "the one object the user sees: one per matter (a pull request, an appointment, a research, the day's agenda), found again by its alias (open_thread gives back the one it matches: take it, or new_because), closed only on its goal; a one-line answer stays text, anything shown is a thread; led (a plan the user approves, steps in parallel, a lead, a budget) for long autonomous work of several steps; every task is a step of one, and another matter asked about has its own"},
}

const conceptMapEN = "memory, intention, skill, monitor, rule, workspace, surface, setting, where, notify, since, task, thread"

var conceptLinesEN = func() string {
	var b strings.Builder
	for _, c := range concepts {
		b.WriteString("- " + c.Name + " (" + c.Tools + "): " + c.When + "\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}()

// The redirecting hints: a request that belongs to another concept.

// notifyPrefRe: wording about how, when or where the user is told. A
// preference of that sort is enforced by code, so it is a rule or a
// setting, not a memory.
// A channel's name is among it (built once the channels are registered).
var notifyPrefRe = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(`(?i)(pr[ée]vien|pr[ée]ven(ir|u|ue)|notif|r[ée]veill|d[ée]rang|heures? calmes?|interromp|ne me (dis|parle|dérange)|pas de messages?|plus de messages?|quiet hours|notify|notification|wake me|disturb|interrupt` +
		channelWords() + `)`)
})

// recurringRe: something that comes back on a schedule.
var recurringRe = regexp.MustCompile(`(?i)(tou(s|tes) les (jours|matins|soirs|semaines|mois|lundis|mardis|mercredis|jeudis|vendredis|samedis|dimanches|week-?ends|\d+ ?(h|heures|minutes|jours))|chaque (jour|matin|soir|semaine|mois|lundi|mardi|mercredi|jeudi|vendredi|samedi|dimanche|week-?end)|every (day|morning|evening|week|month|monday|tuesday|wednesday|thursday|friday|saturday|sunday|\d+ ?(h|hours|minutes|days))|\bdaily\b|\bweekly\b|quotidien|hebdomadaire)`)

const (
	hintSkillNotIntend = "What comes back on a schedule is a skill: an intention fires once. Use write_skill with when: time and a schedule (time daily 07:00, time weekdays 08:00) when the user asks for it."
	hintRuleNotMemory  = "How, when or where the user is told is enforced by code, so it is not a memory: write it as a rule (rule: a topic, or the quiet hours) or a setting (setting: device order, lines off, daily cap, time zone, a channel's detail). recall shows the rules with the memories."

	hintIntentionNotMemory = "What is owed or to do is an intention, with its date: intend (reminder, commitment, promise or waiting_for)."
)

// rememberRedirect is why a memory belongs to another concept, or "".
func rememberRedirect(kind, text string) string {
	switch kind {
	case kindPreference, kindProfile, kindLesson:
		if notifyPrefRe().MatchString(text) {
			return hintRuleNotMemory + " Only if it is about how you behave in the conversation, not about notifications, call remember again with about_conversation."
		}
	case "reminder", "intention", "promise", "commitment", "waiting_for", "todo":
		return hintIntentionNotMemory
	case kindRule, "quiet", "setting":
		return hintRuleNotMemory
	case "watch", "routine", "schedule":
		return hintSkillNotIntend
	}
	return ""
}

// intendHint is why an intention belongs elsewhere, or "".
func intendHint(text string) string {
	if recurringRe.MatchString(text) {
		return hintSkillNotIntend
	}
	return ""
}
