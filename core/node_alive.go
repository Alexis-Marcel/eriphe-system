package main

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// What keeps a node alive: the skills that maintain it (node_home.go)
// and what starts them, the monitors that watch for it (those its skills
// run on and those its blocks read) and what each watches, the sources
// its blocks read (a tool, every N minutes), and when it last changed.
// Its page's ⋯ menu lists it, from what code knows, never from a model's
// words: names are the identifiers the user approved on their cards;
// what each does is said by code from its spec.

// pageAlive is what keeps a node alive, as its page says it.
type pageAlive struct {
	Sources  []aliveLine `json:"sources,omitempty"`
	Monitors []aliveLine `json:"monitors,omitempty"`
	Skills   []aliveLine `json:"skills,omitempty"`
	Updated  time.Time   `json:"updated"`
}

// aliveLine is one of them: its name, and what it does in code's words.
type aliveLine struct {
	Name string `json:"name"`
	What string `json:"what"`
}

// nodeAlive is what keeps node sum alive, read from its last version; nil
// for a node no skill maintains (node_home.go).
func (c *core) nodeAlive(sum nodestore.Summary, lang string) *pageAlive {
	if c.nodes == nil {
		return nil
	}
	a := &pageAlive{Updated: sum.Updated}
	var watched []string
	watch := func(name string) {
		if name != "" && !slices.Contains(watched, name) {
			watched = append(watched, name)
		}
	}
	if d, err := c.nodes.Read(sum.ID, 0); err == nil {
		for _, b := range d.Node.Blocks() {
			for _, name := range slices.Sorted(maps.Keys(b.Sources)) {
				s := b.Sources[name]
				if s.Monitor != "" {
					watch(s.Monitor)
					continue
				}
				a.Sources = append(a.Sources, aliveLine{Name: name, What: aliveSourceText(s, lang)})
			}
		}
	}
	for _, k := range c.triggeredSkills() {
		if k.Trigger == nil || k.Trigger.Node != sum.ID {
			continue
		}
		on := skillMonitor(k.Trigger)
		watch(on)
		a.Skills = append(a.Skills, aliveLine{Name: k.Name, What: skillStartText(k.Trigger, on, lang)})
	}
	for _, name := range watched {
		if m, err := c.monitorNamed(name); err == nil {
			a.Monitors = append(a.Monitors, aliveLine{Name: m.Name, What: m.Spec.Text(lang)})
		}
	}
	if len(a.Skills) == 0 {
		return nil
	}
	return a
}

// aliveSourceText says what a source reads and how often.
func aliveSourceText(s nodefile.Source, lang string) string {
	every := span(time.Duration(cmp.Or(s.Every, sourceEveryDef)) * time.Minute)
	return fmt.Sprintf(tr(lang, "alive.source.every"), sourceText(s), every)
}

// skillMonitor is the monitor a skill's runs start on (its when keyed on
// a monitor's events), "" for none.
func skillMonitor(t *skillTrigger) string {
	if !slices.ContainsFunc(t.Kinds, func(k string) bool { return strings.HasPrefix(k, "monitor.") }) {
		return ""
	}
	return t.Filters["key"]
}

// skillStartText says what starts a skill's runs: a monitor, its events,
// a schedule; at most how often.
func skillStartText(t *skillTrigger, monitor, lang string) string {
	var s string
	switch {
	case monitor != "":
		s = fmt.Sprintf(tr(lang, "alive.skill.monitor"), monitor)
	case len(t.Kinds) > 0:
		s = fmt.Sprintf(tr(lang, "alive.skill.events"), strings.Join(t.Kinds, ", "))
	case t.Schedule != nil:
		s = tr(lang, "alive.skill.schedule")
	default:
		s = tr(lang, "alive.skill.any")
	}
	if t.Every > 0 {
		s += fmt.Sprintf(tr(lang, "alive.skill.every"), span(t.Every))
	}
	return s
}
