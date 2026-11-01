package main

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The router's signals and settings (route.go). The signals are numbers
// and flags: a tab in front, a machine's idle seconds, a device's kind.
// No screen, no app, no microphone. A machine's idle time is kept in
// memory only, a few minutes at most, never in the database.

const (
	routeModeKey    = "route_mode"    // observe (by default) or route
	routeOrderKey   = "route_order"   // the lines in the user's order
	routeOffKey     = "route_off"     // the lines turned off; "none" once the user turned all on
	routeDelayKey   = "route_delay"   // minutes before a "now" not seen goes to the next line
	routeDevicesKey = "route_devices" // the user's say on each subscription: its kind, its node

	idleFresh = 2 * time.Minute // a machine's idle time older than this is unknown
)

// nodeIdle is what a node reports of its machine (GET /v1/node "idle"):
// seconds since the last key or mouse event, and whether the screen is
// locked. Known is false where the node cannot tell (Windows, Linux, a
// container).
type nodeIdle struct {
	Known   bool  `json:"known"`
	Seconds int64 `json:"seconds"`
	Locked  bool  `json:"locked,omitempty"`
	at      time.Time
}

// takeIdle keeps a node's idle time in memory and returns its info
// without it, as it is stored.
func (c *core) takeIdle(name string, info json.RawMessage, now time.Time) json.RawMessage {
	var all map[string]json.RawMessage
	if json.Unmarshal(info, &all) != nil {
		return info
	}
	raw, ok := all["idle"]
	if !ok {
		c.idles.Delete(name)
		return info
	}
	var idle nodeIdle
	if json.Unmarshal(raw, &idle) == nil && idle.Known && idle.Seconds >= 0 {
		idle.at = now
		c.idles.Store(name, idle)
	} else {
		c.idles.Delete(name)
	}
	delete(all, "idle")
	out, err := json.Marshal(all)
	if err != nil {
		return info
	}
	return out
}

// idleOf is a node's idle time now, if it reported one lately.
func (c *core) idleOf(name string, now time.Time) (nodeIdle, bool) {
	v, ok := c.idles.Load(name)
	if !ok {
		return nodeIdle{}, false
	}
	idle := v.(nodeIdle)
	if age := now.Sub(idle.at); age > idleFresh || age < 0 {
		return nodeIdle{}, false
	}
	return idle, true
}

// devicePref is the user's say on a subscription.
type devicePref struct {
	Kind string `json:"kind,omitempty"` // computer or phone; from its label when empty
	Node string `json:"node,omitempty"` // the node on the same machine
}

func (c *core) devicePrefs() map[string]devicePref {
	out := map[string]devicePref{}
	json.Unmarshal([]byte(c.store.get(routeDevicesKey)), &out)
	return out
}

func (c *core) setDevicePref(sub string, change func(*devicePref)) {
	prefs := c.devicePrefs()
	p := prefs[sub]
	change(&p)
	if p == (devicePref{}) {
		delete(prefs, sub)
	} else {
		prefs[sub] = p
	}
	// Subscriptions gone take their say with them.
	if c.push != nil {
		if subs, err := c.push.subscriptions(); err == nil {
			for id := range prefs {
				if !slices.ContainsFunc(subs, func(s pushSub) bool { return s.ID == id }) {
					delete(prefs, id)
				}
			}
		}
	}
	raw, _ := json.Marshal(prefs)
	c.store.set(routeDevicesKey, string(raw))
}

// subKind is a subscription's kind: the user's choice, else its label's.
func subKind(s pushSub, p devicePref) string {
	if p.Kind == kindComputer || p.Kind == kindPhone {
		return p.Kind
	}
	return deviceKind(s.Label, s.UAKind)
}

// routeSettings reads the user's settings.
func (c *core) routeSettings() routeSettings {
	set := routeSettings{Mode: routeObserve, Delay: defaultDelay}
	if c.store.get(routeModeKey) == routeOn {
		set.Mode = routeOn
	}
	for _, s := range splitList(c.store.get(routeOrderKey)) {
		if slices.Contains(routeSteps, s) && !slices.Contains(set.Order, s) {
			set.Order = append(set.Order, s)
		}
	}
	for _, s := range routeSteps {
		if !slices.Contains(set.Order, s) {
			set.Order = append(set.Order, s)
		}
	}
	switch off := c.store.get(routeOffKey); off {
	case "":
		// The default: no voice yet, and the channels the user left out.
		set.Off = []string{stepVoice}
		def := c.defaultChannels()
		if !containsChannel(def, chanTelegram) {
			set.Off = append(set.Off, stepTelegram)
		}
	case "none":
	default:
		set.Off, _ = splitStepsChecked(off)
	}
	if m, err := strconv.Atoi(c.store.get(routeDelayKey)); err == nil && m >= 1 && m <= maxDelayMinutes {
		set.Delay = time.Duration(m) * time.Minute
	}
	return set
}

func splitStepsChecked(s string) ([]string, error) {
	ch, err := checkSteps(s)
	return splitList(ch), err
}

// saveRouteLines keeps the order and the lines off.
func (c *core) saveRouteLines(order, off []string) {
	c.store.set(routeOrderKey, strings.Join(order, ","))
	if len(off) == 0 {
		c.store.set(routeOffKey, "none")
		return
	}
	ch, _ := checkSteps(strings.Join(off, ","))
	c.store.set(routeOffKey, ch)
}

// routeSignals reads the signals now.
func (c *core) routeSignals(now time.Time) routeSignals {
	var sig routeSignals
	if c.tg != nil {
		sig.Telegram = c.tg.linked()
	}
	if cx, err := c.notifyContext(); err == nil {
		_, sig.Quiet = quietAt(cx.Rules, cx.Now)
	}
	if c.push == nil {
		return sig
	}
	subs, _ := c.push.subscriptions()
	prefs := c.devicePrefs()
	tabs := c.push.frontTabs()
	index := map[string]int{}
	for _, s := range subs {
		p := prefs[s.ID]
		d := routeDevice{ID: s.ID, Label: s.Label, Kind: subKind(s, p), App: strings.Contains(s.Label, " · "+tr("fr", "push.app")) ||
			strings.Contains(s.Label, " · "+tr("en", "push.app")), Push: true}
		if p.Node != "" {
			if idle, ok := c.idleOf(p.Node, now); ok {
				d.IdleKnown, d.Idle, d.Locked = true, time.Duration(idle.Seconds)*time.Second, idle.Locked
			}
		}
		index[s.ID] = len(sig.Devices)
		sig.Devices = append(sig.Devices, d)
	}
	ids := make([]string, 0, len(tabs))
	for id := range tabs {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		t := tabs[id]
		if i, ok := index[t.sub]; ok && t.sub != "" {
			sig.Devices[i].Tabs = append(sig.Devices[i].Tabs, id)
			continue
		}
		// A tab whose browser has no subscription: a device of its own.
		kind := t.kind
		if kind == "" {
			kind = kindComputer
		}
		sig.Devices = append(sig.Devices, routeDevice{ID: "tab:" + id, Label: t.label, Kind: kind, Tabs: []string{id}})
	}
	return sig
}

// suggestNode is the node a computer subscription is likely on: the one
// node of its system, if only one.
func suggestNode(label string, nodes []NodeRow) string {
	want := ""
	switch {
	case strings.Contains(label, "Mac"):
		want = "darwin"
	case strings.Contains(label, "Windows"):
		want = "windows"
	case strings.Contains(label, "Linux"):
		want = "linux"
	default:
		return ""
	}
	found := ""
	for _, n := range nodes {
		if n.OS == want {
			if found != "" {
				return ""
			}
			found = n.Name
		}
	}
	return found
}
