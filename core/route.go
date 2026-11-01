package main

import (
	"errors"
	"slices"
	"strings"
	"time"
)

// Where a notification goes: the code decides, never the model. notify
// says what and why (notify.go), decideNotify says when, and route says
// where, from signals eriphe-core already has: a workspace tab in front of
// the user, how long a computer's keyboard and mouse have been idle (its
// node), which push subscription is a phone. One device at a time:
//
//  1. Eriphe open and in front on a device → in the page there, no push;
//  2. a computer in use (idle under two minutes, not locked) → a push to
//     that computer only;
//  3. at home → the voice in the room (not wired yet: off until Home
//     Assistant tells where the user is);
//  4. otherwise → the phone, then Telegram.
//
// The user orders these lines, turns each off, and sets the delay
// (Settings → Où te prévenir). A "now" not seen within the delay (urgent:
// 3 minutes) goes to the next line, two steps at most; nothing else is
// escalated. A topic rule may forbid a line, keep only some, or add one
// ("Getout: the phone too"). The model chooses none of this: no argument
// of notify names a device, and a note from a tainted turn neither adds a
// line nor escalates.

const (
	stepInApp    = "inapp"
	stepComputer = "computer"
	stepVoice    = "voice"
	stepPhone    = "phone"
	stepTelegram = "telegram"

	kindComputer = "computer"
	kindPhone    = "phone"

	routeObserve = "observe" // record what the router would do, send as before
	routeOn      = "route"

	activeIdle      = 2 * time.Minute // a computer idle for less is in use
	defaultDelay    = 10 * time.Minute
	urgentDelay     = 3 * time.Minute
	maxDelayMinutes = 60
	maxEscalation   = 2 // steps after the first
)

// routeSteps are the lines, in their default order.
var routeSteps = []string{stepInApp, stepComputer, stepVoice, stepPhone, stepTelegram}

// stepChannel is the channel of a line, as a rule's channels name them.
func stepChannel(step string) string {
	if step == stepTelegram {
		return chanTelegram
	}
	if step == stepVoice {
		return stepVoice
	}
	return chanPush
}

// checkSteps normalises a list of lines, "phone,telegram": known ones, each
// once, in the default order; "" stays "".
func checkSteps(s string) (string, error) {
	var out []string
	for _, st := range strings.Split(strings.ToLower(s), ",") {
		st = strings.TrimSpace(st)
		switch {
		case st == "":
		case !slices.Contains(routeSteps, st):
			return "", errors.New("lines are inapp, computer, voice, phone or telegram")
		case !slices.Contains(out, st):
			out = append(out, st)
		}
	}
	slices.SortFunc(out, func(a, b string) int { return slices.Index(routeSteps, a) - slices.Index(routeSteps, b) })
	return strings.Join(out, ","), nil
}

func splitList(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

// routeNote is what the router knows of a notification: never its text.
type routeNote struct {
	Level   string `json:"level,omitempty"`   // now escalates; anything else does not
	Urgent  bool   `json:"urgent,omitempty"`  // a rule "even at night": a shorter delay
	Tainted bool   `json:"tainted,omitempty"` // drawn from external content: no line added, no escalation
	Only    string `json:"only,omitempty"`    // a rule's channels: push, telegram
	Also    string `json:"also,omitempty"`    // a rule's lines added to the first
	Never   string `json:"never,omitempty"`   // a rule's lines forbidden
}

// routeDevice is one of the user's devices as the router sees it.
type routeDevice struct {
	ID    string   // its push subscription, or "tab:…" for a tab without one
	Label string   // "Mac · Chrome"
	Kind  string   // computer or phone
	App   bool     // the installed app
	Push  bool     // it has a push subscription
	Tabs  []string // its workspace tabs in front of the user now
	// From the node on the same machine, when one is tied to it.
	IdleKnown  bool
	Idle       time.Duration
	Locked     bool
	Fullscreen bool // a game or a film; not reported yet
}

// routeSignals are the signals, read at the moment of routing.
type routeSignals struct {
	Devices  []routeDevice // the newest subscription first
	Telegram bool          // a Telegram account is linked
	Voice    bool          // Home Assistant says where the user is (not wired yet)
	Quiet    bool          // the quiet hours are on
}

// routeSettings are the user's (Settings → Où te prévenir).
type routeSettings struct {
	Mode  string
	Order []string
	Off   []string
	Delay time.Duration
}

// routeTarget is one place a notification goes.
type routeTarget struct {
	Step   string   `json:"step"`
	Device string   `json:"device,omitempty"`
	Label  string   `json:"label,omitempty"`
	Tabs   []string `json:"tabs,omitempty"`
}

// routePlan is the router's answer: where first, then where if not seen.
type routePlan struct {
	First []routeTarget `json:"first"` // one, plus the lines a rule adds
	Then  []routeTarget `json:"then,omitempty"`
	After time.Duration `json:"after,omitempty"` // between steps; 0: no escalation
}

// route is the policy, a pure function of its inputs. tried are the
// devices (or lines without one) already reached by an earlier step: an
// escalation routes again without them.
func route(n routeNote, sig routeSignals, set routeSettings, tried []string) routePlan {
	order := set.Order
	if len(order) == 0 {
		order = routeSteps
	}
	var picks []routeTarget
	used := slices.Clone(tried)
	for _, step := range order {
		if !lineAllowed(n, set, step) {
			continue
		}
		if t, ok := pickTarget(step, sig, used); ok {
			picks = append(picks, t)
			used = append(used, t.key())
		}
	}
	if len(picks) == 0 && lineAllowed(n, set, stepComputer) {
		// No line reaches the user: rather a computer of theirs than nothing.
		if t, ok := anyComputer(sig, used); ok {
			picks = append(picks, t)
		}
	}
	var p routePlan
	if len(picks) == 0 {
		return p
	}
	p.First = []routeTarget{picks[0]}
	if !n.Tainted {
		p.First = append(p.First, alsoTargets(n, sig, p.First)...)
	}
	// The later steps stand without a fresh signal: a device in use now may
	// not be in ten minutes, and a re-route then sees it.
	for _, t := range picks[1:] {
		reached := slices.ContainsFunc(p.First, func(f routeTarget) bool { return f.key() == t.key() })
		if t.Step != stepInApp && t.Step != stepComputer && !reached && len(p.Then) < maxEscalation {
			p.Then = append(p.Then, t)
		}
	}
	if n.Level == levelNow && !n.Tainted && (!sig.Quiet || n.Urgent) && len(p.Then) > 0 {
		p.After = escalationDelay(n, set)
	} else {
		p.Then = nil
	}
	return p
}

// lineAllowed: the line is on, and the note's rule does not keep it out.
func lineAllowed(n routeNote, set routeSettings, step string) bool {
	if slices.Contains(set.Off, step) || slices.Contains(splitList(n.Never), step) {
		return false
	}
	return n.Only == "" || slices.Contains(splitList(n.Only), stepChannel(step))
}

// alsoTargets are the lines a rule adds to the first step, each on a
// device not reached yet; never a forbidden one.
func alsoTargets(n routeNote, sig routeSignals, first []routeTarget) []routeTarget {
	var out []routeTarget
	used := make([]string, 0, len(first))
	for _, t := range first {
		used = append(used, t.key())
	}
	for _, step := range splitList(n.Also) {
		if slices.Contains(splitList(n.Never), step) || step == stepInApp {
			continue
		}
		if t, ok := pickTarget(step, sig, used); ok {
			out = append(out, t)
			used = append(used, t.key())
		}
	}
	return out
}

// key names what a target reaches, so that no step reaches it twice.
func (t routeTarget) key() string {
	if t.Device != "" {
		return t.Device
	}
	return t.Step
}

// pickTarget is a line's target now, if the line can reach the user.
func pickTarget(step string, sig routeSignals, used []string) (routeTarget, bool) {
	free := func(d routeDevice) bool { return !slices.Contains(used, d.ID) }
	switch step {
	case stepInApp:
		for _, d := range sig.Devices {
			if len(d.Tabs) > 0 && free(d) && !away(d) {
				return routeTarget{Step: step, Device: d.ID, Label: d.Label, Tabs: d.Tabs}, true
			}
		}
	case stepComputer:
		var best *routeDevice
		for i, d := range sig.Devices {
			if d.Kind == kindComputer && d.Push && free(d) && d.IdleKnown && !away(d) && !d.Fullscreen &&
				(best == nil || d.Idle < best.Idle) {
				best = &sig.Devices[i]
			}
		}
		if best != nil {
			return routeTarget{Step: step, Device: best.ID, Label: best.Label}, true
		}
	case stepVoice:
		// Off until Home Assistant tells where the user is (sig.Voice).
	case stepPhone:
		var best *routeDevice
		for i, d := range sig.Devices {
			if d.Kind == kindPhone && d.Push && free(d) && (best == nil || d.App && !best.App) {
				best = &sig.Devices[i]
			}
		}
		if best != nil {
			return routeTarget{Step: step, Device: best.ID, Label: best.Label}, true
		}
	case stepTelegram:
		if sig.Telegram && !slices.Contains(used, stepTelegram) {
			return routeTarget{Step: step, Label: "Telegram"}, true
		}
	}
	return routeTarget{}, false
}

// anyComputer is the last resort: the computer most recently in use, or
// the newest, whatever its idle time.
func anyComputer(sig routeSignals, used []string) (routeTarget, bool) {
	var best *routeDevice
	for i, d := range sig.Devices {
		if d.Kind != kindComputer || !d.Push || slices.Contains(used, d.ID) {
			continue
		}
		if best == nil || d.IdleKnown && (!best.IdleKnown || d.Idle < best.Idle) {
			best = &sig.Devices[i]
		}
	}
	if best == nil {
		return routeTarget{}, false
	}
	return routeTarget{Step: stepComputer, Device: best.ID, Label: best.Label}, true
}

// away: the machine says nobody is at it (idle two minutes, or locked),
// whatever its tabs say: a tab left in front is not the user.
func away(d routeDevice) bool {
	return d.Locked || d.IdleKnown && d.Idle >= activeIdle
}

// deviceKind is a device's kind from its label ("iPhone · Safari"), else
// from its browser's user agent at subscription, else a computer.
func deviceKind(label, uaKind string) string {
	for _, m := range []string{"iPhone", "iPad", "Android"} {
		if strings.Contains(label, m) {
			return kindPhone
		}
	}
	for _, m := range []string{"Mac", "Windows", "Linux"} {
		if strings.Contains(label, m) {
			return kindComputer
		}
	}
	if uaKind == kindPhone {
		return kindPhone
	}
	return kindComputer
}

// uaKind tells a phone's browser from a computer's.
func uaKind(ua string) string {
	for _, m := range []string{"Mobi", "iPhone", "iPad", "Android"} {
		if strings.Contains(ua, m) {
			return kindPhone
		}
	}
	return kindComputer
}
