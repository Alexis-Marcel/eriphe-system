package main

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// A skill's trigger: what makes it run by itself (skill_runs.go). Its
// SKILL.md says it in its front matter, next to name and description:
//
//	when: mail.received                      an event kind (event_journal.go)
//	when: monitor.received key=getout        with filters: the event's key, or a payload field
//	when: monitor.failed, monitor.recovered key=getout-prod   several kinds, the same filters
//	when: time weekdays 08:00                at times of day (rule_schedule.go's schedules)
//	every: 15m                               batch: at most one task per period, with every event since
//	model: haiku                             the agent its tasks run with (Settings → Agents)
//	tools: [mail, calendar]                  the catalog servers its tasks are lent
//	can: [start-session]                     what its tasks may start (#276)
//
// Each is checked when the skill is added: a version with a bad trigger is
// refused. The user approves them with the rest of the skill, as one
// version: changing a trigger is a new version, read with its diff.

const (
	whenTime      = "time"
	canSession    = "start-session"
	minSkillEvery = 5 * time.Minute
	maxSkillEvery = 24 * time.Hour
	maxSkillTools = 10
	maxWhenKinds  = 5
)

var skillCans = []string{canSession}

// skillTrigger is a skill's parsed trigger.
type skillTrigger struct {
	Kind     string            // an event kind (the first), or whenTime
	Kinds    []string          // every event kind it runs on
	Filters  map[string]string // key=…, or a payload field=value
	Schedule *recurrence       // for whenTime
	When     string            // as written
	Every    time.Duration     // 0: at once
	Model    string
	Tools    []string
	Can      []string
	Urgent   string // the fixed text of an urgent run (skill_cards.go)
}

var (
	skillEveryRe  = regexp.MustCompile(`^(\d{1,4})\s*(m|min|h|d|j)$`)
	filterFieldRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,30}$`)
)

// parseSkillTrigger reads a manifest's trigger keys; nil when it has none.
// The keys other than when need it: a skill without when runs only when
// someone follows it.
func parseSkillTrigger(m skillManifest) (*skillTrigger, error) {
	if m.When == "" {
		for k, v := range map[string]string{"every": m.Every, "model": m.Model, "tools": m.Tools, "can": m.Can, "urgent": m.Urgent} {
			if v != "" {
				return nil, skillErr("skills.err.nowhen", k)
			}
		}
		return nil, nil
	}
	st := &skillTrigger{When: m.When, Model: strings.TrimSpace(m.Model)}
	words := strings.Fields(m.When)
	if words[0] != whenTime {
		// The event kinds come first, by commas or spaces, then the filters.
		var kinds []string
		for len(words) > 0 && !strings.Contains(words[0], "=") {
			for _, k := range strings.Split(words[0], ",") {
				if k != "" && !slices.Contains(kinds, k) {
					kinds = append(kinds, k)
				}
			}
			words = words[1:]
		}
		if len(kinds) == 0 || len(kinds) > maxWhenKinds {
			return nil, skillErr("skills.err.when", m.When)
		}
		words = append([]string{kinds[0]}, words...)
		st.Kinds = kinds
	}
	st.Kind = words[0]
	switch {
	case st.Kind == whenTime:
		r, err := parseSchedule(strings.Join(words[1:], " "))
		if err != nil {
			return nil, skillErr("skills.err.when", m.When)
		}
		st.Schedule = &r
	case slices.ContainsFunc(st.Kinds, func(k string) bool { return !eventKindRe.MatchString(k) || k == whenTime }):
		return nil, skillErr("skills.err.when", m.When)
	default:
		for _, w := range words[1:] {
			k, v, ok := strings.Cut(w, "=")
			if !ok || !filterFieldRe.MatchString(k) || v == "" {
				return nil, skillErr("skills.err.when", m.When)
			}
			if st.Filters == nil {
				st.Filters = map[string]string{}
			}
			st.Filters[k] = v
		}
	}
	if m.Every != "" {
		d, err := parseSkillEvery(m.Every)
		if err != nil || st.Kind == whenTime {
			return nil, skillErr("skills.err.every", m.Every)
		}
		st.Every = d
	}
	st.Tools = frontList(m.Tools)
	if len(st.Tools) > maxSkillTools {
		return nil, skillErr("skills.err.tools", m.Tools)
	}
	for _, t := range st.Tools {
		if !serverName.MatchString(t) {
			return nil, skillErr("skills.err.tools", t)
		}
	}
	if u := strings.TrimSpace(m.Urgent); u != "" {
		if utf8.RuneCountInString(u) > urgentTextMax || urlRe2.MatchString(u) {
			return nil, skillErr("skills.err.urgent", u)
		}
		st.Urgent = u
	}
	st.Can = frontList(m.Can)
	for _, c := range st.Can {
		if !slices.Contains(skillCans, c) {
			return nil, skillErr("skills.err.can", c)
		}
	}
	return st, nil
}

// parseSkillEvery reads 15m, 2h, 1d (or 1j), between 5 minutes and a day.
func parseSkillEvery(s string) (time.Duration, error) {
	m := skillEveryRe.FindStringSubmatch(strings.ToLower(strings.TrimSpace(s)))
	if m == nil {
		return 0, fmt.Errorf("every %q: a number and m, h or d", s)
	}
	n, _ := strconv.Atoi(m[1])
	d := time.Duration(n) * time.Minute
	switch m[2] {
	case "h":
		d = time.Duration(n) * time.Hour
	case "d", "j":
		d = time.Duration(n) * 24 * time.Hour
	}
	if d < minSkillEvery || d > maxSkillEvery {
		return 0, fmt.Errorf("every %q: between 5m and 1d", s)
	}
	return d, nil
}

// frontList reads a front matter list: [a, b], a, b, or its indented lines
// (parseSkillMD has joined them with spaces).
func frontList(s string) []string {
	s = strings.Trim(strings.TrimSpace(s), "[]")
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		if f = strings.Trim(strings.TrimSpace(f), `"'`); f != "" && !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	return out
}

// matches tells whether an event fires the trigger.
func (st *skillTrigger) matches(e journalEvent) bool {
	if st.Kind == whenTime || !slices.Contains(st.Kinds, e.Kind) {
		return false
	}
	for k, want := range st.Filters {
		got := e.Key
		if k != "key" {
			v, ok := e.Payload[k]
			if !ok {
				return false
			}
			got = fmt.Sprint(v)
		}
		if !strings.EqualFold(got, want) {
			return false
		}
	}
	return true
}

// Text says the trigger in words, for the skill's page and its card.
func (st *skillTrigger) Text(lang string) string {
	var parts []string
	if st.Kind == whenTime {
		parts = append(parts, fmt.Sprintf(tr(lang, "skills.trigger.time"), strings.TrimSpace(strings.TrimPrefix(st.When, whenTime))))
	} else {
		parts = append(parts, fmt.Sprintf(tr(lang, "skills.trigger.event"), st.When))
	}
	if st.Every > 0 {
		parts = append(parts, fmt.Sprintf(tr(lang, "skills.trigger.every"), span(st.Every)))
	}
	if st.Model != "" {
		parts = append(parts, fmt.Sprintf(tr(lang, "skills.trigger.model"), st.Model))
	}
	if len(st.Tools) > 0 {
		parts = append(parts, fmt.Sprintf(tr(lang, "skills.trigger.tools"), strings.Join(st.Tools, ", ")))
	}
	if slices.Contains(st.Can, canSession) {
		parts = append(parts, tr(lang, "skills.trigger.session"))
	}
	if st.Urgent != "" {
		parts = append(parts, fmt.Sprintf(tr(lang, "skills.trigger.urgent"), st.Urgent))
	}
	return strings.Join(parts, " · ")
}
