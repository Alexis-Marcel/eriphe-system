package main

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Settings → Notifications → Où te prévenir: the router's page (route.go). The lines in
// the user's order, one switch each, shown when what they need is there;
// the delay before the next one; observe or route; which subscription is
// a phone or a computer, and which node is on which computer; and the
// last decisions, planned next to sent.

func (w *web) whereRoutes(mux *http.ServeMux) {
	mux.Handle("POST /where/mode", w.auth(w.handleWhereMode))
	mux.Handle("POST /where/line", w.auth(w.handleWhereLine))
	mux.Handle("POST /where/delay", w.auth(w.handleWhereDelay))
	mux.Handle("POST /where/device", w.auth(w.handleWhereDevice))
	mux.Handle("POST /where/node", w.auth(w.handleWhereNode))
}

type whereLine struct {
	Step        string
	On          bool
	Usable      bool   // what it needs is there
	Hint        string // what it needs, when it is not
	First, Last bool   // among the lines shown
}

type whereDevice struct {
	ID, Label, Kind string
	Choice          string // the user's: computer, phone, or empty (from its label)
}

type whereMachine struct {
	Name, OS  string
	Sub       string // the subscription tied to it
	Suggested string // the one that matches its system, when none is
	IdleKnown bool
	Idle      int64
	Locked    bool
}

type whereEntry struct {
	At      time.Time
	What    string
	Mode    string
	Planned string
	Sent    string
	Seen    string // how, or empty
}

type wherePage struct {
	Route     bool // route mode, else observe
	Lines     []whereLine
	Delay     int
	Devices   []whereDevice
	Computers []whereDevice
	Nodes     []whereMachine
	Log       []whereEntry
}

func (w *web) wherePage(lang string) wherePage {
	c := w.c
	set := c.routeSettings()
	v := wherePage{Route: set.Mode == routeOn, Delay: int(set.Delay / time.Minute)}
	prefs := c.devicePrefs()
	hasKind := map[string]bool{}
	if c.push != nil {
		subs, _ := c.push.subscriptions()
		for _, s := range subs {
			d := whereDevice{ID: s.ID, Label: s.Label, Kind: subKind(s, prefs[s.ID]), Choice: prefs[s.ID].Kind}
			hasKind[d.Kind] = true
			v.Devices = append(v.Devices, d)
			if d.Kind == kindComputer {
				v.Computers = append(v.Computers, d)
			}
		}
	}
	telegram := c.tg != nil && c.tg.linked()
	for _, step := range set.Order {
		l := whereLine{Step: step, On: !slices.Contains(set.Off, step), Usable: true}
		switch step {
		case stepComputer:
			if !hasKind[kindComputer] {
				continue
			}
		case stepVoice:
			l.Usable, l.Hint = false, "where.hint.voice"
		case stepPhone:
			if !hasKind[kindPhone] {
				l.Usable, l.Hint = false, "where.hint.phone"
			}
		case stepTelegram:
			if !telegram {
				continue
			}
		}
		v.Lines = append(v.Lines, l)
	}
	if n := len(v.Lines); n > 0 {
		v.Lines[0].First, v.Lines[n-1].Last = true, true
	}
	v.Nodes = w.whereNodes(v.Computers, prefs)
	for _, e := range c.recentRoutes(20) {
		v.Log = append(v.Log, whereEntry{At: e.At, What: e.What, Mode: e.Mode, Planned: describePlan(lang, e.Planned),
			Sent: describeSent(lang, e.Sent, e.Skip), Seen: e.AckedHow})
	}
	return v
}

// whereNodes are the user's machines, each with the subscription tied to it.
func (w *web) whereNodes(computers []whereDevice, prefs map[string]devicePref) []whereMachine {
	nodes, err := w.c.store.nodes()
	if err != nil {
		return nil
	}
	var out []whereMachine
	now := time.Now()
	for _, n := range nodes {
		wn := whereMachine{Name: n.Name, OS: n.OS}
		for _, d := range computers {
			if prefs[d.ID].Node == n.Name {
				wn.Sub = d.ID
			}
		}
		if wn.Sub == "" {
			var match []string
			for _, d := range computers {
				if prefs[d.ID].Node == "" && suggestNode(d.Label, nodes) == n.Name {
					match = append(match, d.ID)
				}
			}
			if len(match) == 1 {
				wn.Suggested = match[0]
			}
		}
		if idle, ok := w.c.idleOf(n.Name, now); ok {
			wn.IdleKnown, wn.Idle, wn.Locked = true, idle.Seconds, idle.Locked
		}
		out = append(out, wn)
	}
	return out
}

// describeTarget words a target: "sur ce Mac · Chrome, dans l'appli".
func describeTarget(lang string, t routeTarget) string {
	s := tr(lang, "where.step."+t.Step)
	if t.Label != "" && t.Step != stepTelegram {
		s += " · " + t.Label
	}
	return s
}

func describeTargets(lang string, ts []routeTarget) string {
	parts := make([]string, len(ts))
	for i, t := range ts {
		parts[i] = describeTarget(lang, t)
	}
	return strings.Join(parts, " + ")
}

// describePlan words a plan: first, then each next step with its delay.
func describePlan(lang string, p routePlan) string {
	if len(p.First) == 0 {
		return tr(lang, "where.nowhere")
	}
	s := describeTargets(lang, p.First)
	for _, t := range p.Then {
		s += fmt.Sprintf(" → (%d min) ", int(p.After/time.Minute)) + describeTarget(lang, t)
	}
	return s
}

func describeSent(lang string, sent []routeTarget, skip string) string {
	if skip == skipFront {
		return tr(lang, "where.skip.front")
	}
	if len(sent) == 0 {
		return tr(lang, "where.nowhere")
	}
	parts := make([]string, len(sent))
	for i, t := range sent {
		parts[i] = describeTarget(lang, t)
	}
	return strings.Join(parts, ", ")
}

func (w *web) renderWhere(rw http.ResponseWriter, r *http.Request, s *session, msg string) {
	rw.Header().Set("HX-Trigger", hxTrigger(msg, true, false))
	w.render(rw, r, s, "settings", "where-frag", page{Data: w.wherePage(w.lang(r))})
}

func (w *web) handleWhereMode(rw http.ResponseWriter, r *http.Request, s *session) {
	mode := routeObserve
	if r.PostFormValue("mode") == routeOn {
		mode = routeOn
	}
	w.c.store.set(routeModeKey, mode)
	w.renderWhere(rw, r, s, tr(w.lang(r), "where.saved.mode."+mode))
}

// handleWhereLine turns a line on or off, or moves it up or down past
// the next line shown.
func (w *web) handleWhereLine(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	step := r.PostFormValue("step")
	if !slices.Contains(routeSteps, step) {
		http.Error(rw, "unknown line", http.StatusBadRequest)
		return
	}
	set := w.c.routeSettings()
	order, off := set.Order, set.Off
	action := r.PostFormValue("action")
	if action == "" { // a line's switch
		action = "off"
		if r.PostFormValue("on") == "1" {
			action = "on"
		}
	}
	switch action {
	case "on":
		off = slices.DeleteFunc(off, func(s string) bool { return s == step })
	case "off":
		if !slices.Contains(off, step) {
			off = append(off, step)
		}
	case "up", "down":
		shown := map[string]bool{}
		for _, l := range w.wherePage(lang).Lines {
			shown[l.Step] = true
		}
		order = moveLine(order, step, action == "up", shown)
	default:
		http.Error(rw, "on, off, up or down", http.StatusBadRequest)
		return
	}
	w.c.saveRouteLines(order, off)
	w.renderWhere(rw, r, s, tr(lang, "where.saved"))
}

// moveLine moves a line past the next one shown, above or below; the
// lines hidden keep their places relative to the others.
func moveLine(order []string, step string, up bool, shown map[string]bool) []string {
	i := slices.Index(order, step)
	if i < 0 {
		return slices.Clone(order)
	}
	d := 1
	if up {
		d = -1
	}
	for j := i + d; j >= 0 && j < len(order); j += d {
		if !shown[order[j]] {
			continue
		}
		rest := slices.Delete(slices.Clone(order), i, i+1)
		at := slices.Index(rest, order[j])
		if !up {
			at++
		}
		return slices.Insert(rest, at, step)
	}
	return slices.Clone(order)
}

func (w *web) handleWhereDelay(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	m, err := strconv.Atoi(r.PostFormValue("delay"))
	if err != nil || m < 1 || m > maxDelayMinutes {
		w.toast(rw, tr(lang, "where.err.delay"), false)
		return
	}
	w.c.store.set(routeDelayKey, strconv.Itoa(m))
	w.renderWhere(rw, r, s, tr(lang, "where.saved"))
}

func (w *web) handleWhereDevice(rw http.ResponseWriter, r *http.Request, s *session) {
	sub, kind := r.PostFormValue("sub"), r.PostFormValue("kind")
	if !isPushID(sub) || kind != "" && kind != kindComputer && kind != kindPhone {
		http.Error(rw, "bad device", http.StatusBadRequest)
		return
	}
	w.c.setDevicePref(sub, func(p *devicePref) { p.Kind = kind })
	w.renderWhere(rw, r, s, tr(w.lang(r), "where.saved"))
}

// handleWhereNode ties a node to the subscription on the same machine,
// or unties it (sub empty).
func (w *web) handleWhereNode(rw http.ResponseWriter, r *http.Request, s *session) {
	node, sub := r.PostFormValue("node"), r.PostFormValue("sub")
	nodes, err := w.c.store.nodes()
	if err != nil || !slices.ContainsFunc(nodes, func(n NodeRow) bool { return n.Name == node }) || sub != "" && !isPushID(sub) {
		http.Error(rw, "bad node", http.StatusBadRequest)
		return
	}
	for id, p := range w.c.devicePrefs() {
		if p.Node == node && id != sub {
			w.c.setDevicePref(id, func(p *devicePref) { p.Node = "" })
		}
	}
	if sub != "" {
		w.c.setDevicePref(sub, func(p *devicePref) { p.Node = node })
	}
	w.renderWhere(rw, r, s, tr(w.lang(r), "where.saved"))
}
