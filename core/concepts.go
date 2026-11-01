package main

import (
	"regexp"
	"strings"
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
	{"watch", "watch, watches, update_watch", "what comes back on its own: at times of day (horaire), on the user's return (retour), when a tool's result changes (outil), when a service alerts (alerte)"},
	{"rule", "rule", "what to do with notifications about a topic (now, brief, never, even at night, which lines), and the quiet hours"},
	{"setting", "setting, presence_sources, map_presence", "how the user is told in general: device order, lines off, daily cap, time zone, Telegram detail; what you may know of the user, and which Home Assistant entities tell it"},
	{"notify", "notify, notifications", "say something on your own, now or for the brief, under a known topic ref"},
	{"since", "since", "what happened since a time, as eriphe-core recorded it"},
	{"task", "run_task", "long or heavy work, by an agent on a machine"},
}

const conceptMapEN = "memory, intention, watch, rule, setting, notify, since, task"

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
var notifyPrefRe = regexp.MustCompile(`(?i)(pr[ée]vien|pr[ée]ven(ir|u|ue)|notif|r[ée]veill|d[ée]rang|heures? calmes?|interromp|ne me (dis|parle|dérange)|pas de messages?|plus de messages?|telegram|quiet hours|notify|notification|wake me|disturb|interrupt)`)

// recurringRe: something that comes back on a schedule.
var recurringRe = regexp.MustCompile(`(?i)(tou(s|tes) les (jours|matins|soirs|semaines|mois|lundis|mardis|mercredis|jeudis|vendredis|samedis|dimanches|week-?ends|\d+ ?(h|heures|minutes|jours))|chaque (jour|matin|soir|semaine|mois|lundi|mardi|mercredi|jeudi|vendredi|samedi|dimanche|week-?end)|every (day|morning|evening|week|month|monday|tuesday|wednesday|thursday|friday|saturday|sunday|\d+ ?(h|hours|minutes|days))|\bdaily\b|\bweekly\b|quotidien|hebdomadaire)`)

// oneOffRe: a schedule that names one moment, not a recurrence.
var oneOffRe = regexp.MustCompile(`(?i)(\d{4}-\d{2}-\d{2}|\b\d{1,2}/\d{1,2}\b|demain|aujourd'?hui|ce soir|tomorrow|today|tonight|\bonce\b|une fois|dans \d+|\bin \d+|\ble \d{1,2}\b|\bon the \d)`)

const (
	hintIntendNotWatch = "A watch comes back on a schedule; a one-off time is an intention: intend (kind reminder, remind_at YYYY-MM-DDTHH:MM)."
	hintWatchNotIntend = "What comes back on a schedule is a watch: an intention fires once. Use watch (source horaire, schedule like daily 07:00 or monday 09:00) when the user asks for it."
	hintRuleNotMemory  = "How, when or where the user is told is enforced by code, so it is not a memory: write it as a rule (rule: a topic, or the quiet hours) or a setting (setting: device order, lines off, daily cap, time zone, Telegram detail). recall shows the rules with the memories."

	hintIntentionNotMemory = "What is owed or to do is an intention, with its date: intend (reminder, commitment, promise or waiting_for)."
)

// rememberRedirect is why a memory belongs to another concept, or "".
func rememberRedirect(kind, text string) string {
	switch kind {
	case kindPreference, kindProfile, kindLesson:
		if notifyPrefRe.MatchString(text) {
			return hintRuleNotMemory + " Only if it is about how you behave in the conversation, not about notifications, call remember again with about_conversation."
		}
	case "reminder", "intention", "promise", "waiting_for", "todo":
		return hintIntentionNotMemory
	case kindRule, "quiet", "setting":
		return hintRuleNotMemory
	case "watch", "routine", "schedule":
		return hintWatchNotIntend
	}
	return ""
}

// intendHint is why an intention belongs elsewhere, or "".
func intendHint(text string) string {
	if recurringRe.MatchString(text) {
		return hintWatchNotIntend
	}
	return ""
}

// watchScheduleHint is why a schedule belongs to an intention, or "".
func watchScheduleHint(schedule string) string {
	if oneOffRe.MatchString(schedule) {
		return hintIntendNotWatch
	}
	return ""
}
