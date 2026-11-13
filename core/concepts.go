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
	{"memory", "remember, recall", "what you must know about the user: facts, people, decisions, a project's progress, how they want you to talk"},
	{"intention", "intend, intentions, update_intention", "something owed or to do once: at a date and time (remind me at 14:00), or the next time a name comes up"},
	{"skill", "write_skill, list_skills, read_skill", "what comes back or reacts on its own: a skill with when (an event: a mail, an alert, the user's return, a task's end; or a time) runs as a task in the background and tells the user with its last message; also know-how followed when asked"},
	{"monitor", "write_monitor, monitors", "know that something changed in a tool that does not tell by itself (Grafana's alerts, a status page): eriphe-core calls it at an interval, without a model, and writes an event a skill reacts to; never a time skill that calls the tool"},
	{"rule", "rule", "what to do with notifications about a topic (now, brief, never, even at night, which lines), and the quiet hours"},
	{"workspace", "workspace_arrange, workspace_focus", "the composed workspace: arrange its surfaces as the user asks (a size, a place, for their session); put on its stage the surface that answers what they asked from it (workspace_focus); how the user wants it in general (what in front, what less, what never) is a preference memory with subject workspace, which its composer reads"},
	{"surface", "surface_write, surface_remove, workspace_read, catalog_read", "show the user something on their workspace when they ask to see it: a surface (a tile) live from any tool you read freely (sources), or composed by a task (a placeholder at once, run_task with surface); what they want to see every morning is a skill that keeps its surface; components are shapes, never tool names"},
	{"setting", "setting", "how the user is told in general: device order, lines off, daily cap, time zone, a channel's detail, an urgent skill said aloud"},
	{"where", "where_now", "where a notification would reach the user now: lines, devices, the room a voice channel says they are in"},
	{"notify", "notify, notifications", "say something on your own, now or for the brief, under a known topic ref"},
	{"since", "since", "what happened since a time, as eriphe-core recorded it"},
	{"task", "run_task", "long or heavy work, by an agent on a machine"},
	{"thread", "open_thread, find_thread, read_thread, close_thread, reopen_thread", "one matter followed to its real goal (a pull request, an appointment, a brand name to find): opened light at once, found again by its subject, closed only on its goal; led (a plan the user approves, steps in parallel, a lead, a budget) for long autonomous work of several steps"},
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
